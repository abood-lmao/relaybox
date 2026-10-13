// Package settings manages runtime settings: values stored in the database
// that take effect immediately, without a restart. Environment variables only
// provide their defaults.
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/textproto"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/abood-lmao/relaybox/internal/config"
	"github.com/abood-lmao/relaybox/internal/redact"
	"github.com/abood-lmao/relaybox/internal/validate"
)

// Setting keys as stored in the settings table and used in the API.
const (
	KeyRetentionDays       = "retention_days"
	KeyMaxStoredRequests   = "max_stored_requests"
	KeyDefaultMaxBodyBytes = "default_max_body_bytes"
	KeyRedactHeaders       = "redact_headers"
	KeyRedactFields        = "redact_fields"
)

// AllKeys lists every runtime setting.
var AllKeys = []string{KeyRetentionDays, KeyMaxStoredRequests, KeyDefaultMaxBodyBytes, KeyRedactHeaders, KeyRedactFields}

const (
	maxListEntries = 100
	maxEntryLength = 100
)

// Values are the runtime settings.
type Values struct {
	RetentionDays       int      `json:"retention_days"`
	MaxStoredRequests   int      `json:"max_stored_requests"`
	DefaultMaxBodyBytes int64    `json:"default_max_body_bytes"`
	RedactHeaders       []string `json:"redact_headers"`
	RedactFields        []string `json:"redact_fields"`
}

// Snapshot is an immutable view of the current settings.
type Snapshot struct {
	Values
	Policy     redact.Policy
	Overridden []string // keys whose value differs from the environment default
}

// Patch holds requested changes. Nil fields stay unchanged; keys in Reset
// return to their defaults.
type Patch struct {
	RetentionDays       *int      `json:"retention_days,omitempty"`
	MaxStoredRequests   *int      `json:"max_stored_requests,omitempty"`
	DefaultMaxBodyBytes *int64    `json:"default_max_body_bytes,omitempty"`
	RedactHeaders       *[]string `json:"redact_headers,omitempty"`
	RedactFields        *[]string `json:"redact_fields,omitempty"`
	Reset               []string  `json:"reset,omitempty"`
}

// Store persists settings.
type Store interface {
	LoadSettings(ctx context.Context) (map[string]string, error)
	SaveSettings(ctx context.Context, values map[string]string, remove []string) error
}

// Service holds the current settings and applies updates.
type Service struct {
	store    Store
	defaults Values
	ceiling  int64
	mu       sync.Mutex
	cur      atomic.Pointer[Snapshot]
}

// DefaultsFromConfig derives default values from process configuration.
func DefaultsFromConfig(cfg config.Config) Values {
	return Values{
		RetentionDays:       cfg.RetentionDays,
		MaxStoredRequests:   cfg.MaxStoredRequests,
		DefaultMaxBodyBytes: cfg.MaxBodyBytes,
		RedactHeaders:       slices.Clone(redact.DefaultHeaders),
		RedactFields:        slices.Clone(redact.DefaultFields),
	}
}

// New loads stored settings. Stored values that are no longer valid (for
// example a body limit above a lowered RELAYBOX_MAX_BODY_BYTES) fall back to
// the default and are reported in the returned warnings.
func New(ctx context.Context, store Store, defaults Values, ceiling int64) (*Service, []string, error) {
	s := &Service{store: store, defaults: defaults, ceiling: ceiling}
	stored, err := store.LoadSettings(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("load settings: %w", err)
	}
	var warnings []string
	v := cloneValues(defaults)
	for key, raw := range stored {
		candidate := cloneValues(v)
		if err := decodeInto(&candidate, key, raw); err != nil {
			warnings = append(warnings, fmt.Sprintf("ignoring stored setting %s: %v", key, err))
			continue
		}
		if errs := s.check(candidate); len(errs) > 0 {
			warnings = append(warnings, fmt.Sprintf("ignoring stored setting %s: %s", key, errs[key]))
			continue
		}
		v = candidate
	}
	s.cur.Store(s.snapshot(v))
	return s, warnings, nil
}

// Current returns the active settings.
func (s *Service) Current() *Snapshot { return s.cur.Load() }

// Defaults returns the environment-derived defaults.
func (s *Service) Defaults() Values { return cloneValues(s.defaults) }

// Ceiling returns the largest allowed body limit (RELAYBOX_MAX_BODY_BYTES).
func (s *Service) Ceiling() int64 { return s.ceiling }

// Update validates and applies a patch. On validation failure it returns
// validate.Errors and changes nothing.
func (s *Service) Update(ctx context.Context, p Patch) (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := cloneValues(s.cur.Load().Values)
	errs := validate.Errors{}
	for _, key := range p.Reset {
		if !slices.Contains(AllKeys, key) {
			errs.Add("reset", fmt.Sprintf("%q is not a runtime setting.", key))
			continue
		}
		applyDefault(&next, s.defaults, key)
	}
	if p.RetentionDays != nil {
		next.RetentionDays = *p.RetentionDays
	}
	if p.MaxStoredRequests != nil {
		next.MaxStoredRequests = *p.MaxStoredRequests
	}
	if p.DefaultMaxBodyBytes != nil {
		next.DefaultMaxBodyBytes = *p.DefaultMaxBodyBytes
	}
	if p.RedactHeaders != nil {
		next.RedactHeaders = normalizeHeaders(*p.RedactHeaders)
	}
	if p.RedactFields != nil {
		next.RedactFields = normalizeFields(*p.RedactFields)
	}
	for k, msg := range s.check(next) {
		errs.Add(k, msg)
	}
	if len(errs) > 0 {
		return nil, errs
	}

	values := make(map[string]string)
	var remove []string
	for _, key := range AllKeys {
		if equalField(next, s.defaults, key) {
			remove = append(remove, key)
			continue
		}
		raw, err := encodeField(next, key)
		if err != nil {
			return nil, err
		}
		values[key] = raw
	}
	if err := s.store.SaveSettings(ctx, values, remove); err != nil {
		return nil, fmt.Errorf("save settings: %w", err)
	}
	snap := s.snapshot(next)
	s.cur.Store(snap)
	return snap, nil
}

func (s *Service) snapshot(v Values) *Snapshot {
	var overridden []string
	for _, key := range AllKeys {
		if !equalField(v, s.defaults, key) {
			overridden = append(overridden, key)
		}
	}
	return &Snapshot{
		Values:     v,
		Policy:     redact.NewPolicy(v.RedactHeaders, v.RedactFields),
		Overridden: overridden,
	}
}

func (s *Service) check(v Values) validate.Errors {
	errs := validate.Errors{}
	if v.RetentionDays < 0 || v.RetentionDays > config.MaxRetentionDays {
		errs.Add(KeyRetentionDays, fmt.Sprintf("Retention must be between 0 (keep forever) and %d days.", config.MaxRetentionDays))
	}
	if v.MaxStoredRequests < 0 || v.MaxStoredRequests > config.MaxStoredRequestsLimit {
		errs.Add(KeyMaxStoredRequests, fmt.Sprintf("Maximum stored requests must be between 0 (no limit) and %d.", config.MaxStoredRequestsLimit))
	}
	if v.DefaultMaxBodyBytes < config.MinBodyBytes || v.DefaultMaxBodyBytes > s.ceiling {
		errs.Add(KeyDefaultMaxBodyBytes, fmt.Sprintf("Body limit must be between %d and %d bytes (RELAYBOX_MAX_BODY_BYTES).", config.MinBodyBytes, s.ceiling))
	}
	if len(v.RedactHeaders) > maxListEntries {
		errs.Add(KeyRedactHeaders, fmt.Sprintf("At most %d header names are allowed.", maxListEntries))
	}
	for _, h := range v.RedactHeaders {
		if !validate.IsToken(h) || len(h) > maxEntryLength {
			errs.Add(KeyRedactHeaders, fmt.Sprintf("%q is not a valid header name.", h))
		}
	}
	if len(v.RedactFields) > maxListEntries {
		errs.Add(KeyRedactFields, fmt.Sprintf("At most %d field names are allowed.", maxListEntries))
	}
	for _, f := range v.RedactFields {
		if redact.NormalizeField(f) == "" || len(f) > maxEntryLength || !validate.IsHeaderValue(f) {
			errs.Add(KeyRedactFields, fmt.Sprintf("%q is not a valid field name.", f))
		}
	}
	return errs
}

func normalizeHeaders(in []string) []string {
	out := []string{}
	seen := make(map[string]bool)
	for _, h := range in {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if validate.IsToken(h) {
			h = textproto.CanonicalMIMEHeaderKey(h)
		}
		if key := strings.ToLower(h); !seen[key] {
			seen[key] = true
			out = append(out, h)
		}
	}
	return out
}

func normalizeFields(in []string) []string {
	out := []string{}
	seen := make(map[string]bool)
	for _, f := range in {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if key := redact.NormalizeField(f); !seen[key] {
			seen[key] = true
			out = append(out, f)
		}
	}
	return out
}

func cloneValues(v Values) Values {
	v.RedactHeaders = slices.Clone(v.RedactHeaders)
	v.RedactFields = slices.Clone(v.RedactFields)
	if v.RedactHeaders == nil {
		v.RedactHeaders = []string{}
	}
	if v.RedactFields == nil {
		v.RedactFields = []string{}
	}
	return v
}

func applyDefault(v *Values, d Values, key string) {
	switch key {
	case KeyRetentionDays:
		v.RetentionDays = d.RetentionDays
	case KeyMaxStoredRequests:
		v.MaxStoredRequests = d.MaxStoredRequests
	case KeyDefaultMaxBodyBytes:
		v.DefaultMaxBodyBytes = d.DefaultMaxBodyBytes
	case KeyRedactHeaders:
		v.RedactHeaders = slices.Clone(d.RedactHeaders)
	case KeyRedactFields:
		v.RedactFields = slices.Clone(d.RedactFields)
	}
}

func equalField(a, b Values, key string) bool {
	switch key {
	case KeyRetentionDays:
		return a.RetentionDays == b.RetentionDays
	case KeyMaxStoredRequests:
		return a.MaxStoredRequests == b.MaxStoredRequests
	case KeyDefaultMaxBodyBytes:
		return a.DefaultMaxBodyBytes == b.DefaultMaxBodyBytes
	case KeyRedactHeaders:
		return slices.Equal(a.RedactHeaders, b.RedactHeaders)
	case KeyRedactFields:
		return slices.Equal(a.RedactFields, b.RedactFields)
	}
	return false
}

func encodeField(v Values, key string) (string, error) {
	var x any
	switch key {
	case KeyRetentionDays:
		x = v.RetentionDays
	case KeyMaxStoredRequests:
		x = v.MaxStoredRequests
	case KeyDefaultMaxBodyBytes:
		x = v.DefaultMaxBodyBytes
	case KeyRedactHeaders:
		x = v.RedactHeaders
	case KeyRedactFields:
		x = v.RedactFields
	default:
		return "", fmt.Errorf("unknown setting %q", key)
	}
	b, err := json.Marshal(x)
	return string(b), err
}

func decodeInto(v *Values, key, raw string) error {
	var dst any
	switch key {
	case KeyRetentionDays:
		dst = &v.RetentionDays
	case KeyMaxStoredRequests:
		dst = &v.MaxStoredRequests
	case KeyDefaultMaxBodyBytes:
		dst = &v.DefaultMaxBodyBytes
	case KeyRedactHeaders:
		dst = &v.RedactHeaders
	case KeyRedactFields:
		dst = &v.RedactFields
	default:
		return fmt.Errorf("unknown setting")
	}
	return json.Unmarshal([]byte(raw), dst)
}
