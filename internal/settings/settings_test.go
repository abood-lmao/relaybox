package settings

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/abood-lmao/relaybox/internal/validate"
)

type memStore struct {
	data  map[string]string
	saves int
}

func (m *memStore) LoadSettings(context.Context) (map[string]string, error) {
	out := make(map[string]string, len(m.data))
	for k, v := range m.data {
		out[k] = v
	}
	return out, nil
}

func (m *memStore) SaveSettings(_ context.Context, values map[string]string, remove []string) error {
	m.saves++
	for k, v := range values {
		m.data[k] = v
	}
	for _, k := range remove {
		delete(m.data, k)
	}
	return nil
}

func testDefaults() Values {
	return Values{
		RetentionDays:       7,
		MaxStoredRequests:   10000,
		DefaultMaxBodyBytes: 1 << 20,
		RedactHeaders:       []string{"Authorization"},
		RedactFields:        []string{"password"},
	}
}

func TestDefaultsApplyWhenNothingStored(t *testing.T) {
	s, warnings, err := New(context.Background(), &memStore{data: map[string]string{}}, testDefaults(), 1<<20)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("New: %v %v", err, warnings)
	}
	cur := s.Current()
	if cur.RetentionDays != 7 || len(cur.Overridden) != 0 {
		t.Fatalf("unexpected snapshot %+v", cur)
	}
	if !cur.Policy.HeaderRedacted("authorization") || !cur.Policy.FieldRedacted("Password") {
		t.Fatal("policy does not reflect defaults")
	}
}

func TestUpdatePersistsAndAppliesImmediately(t *testing.T) {
	store := &memStore{data: map[string]string{}}
	s, _, _ := New(context.Background(), store, testDefaults(), 1<<20)

	days := 30
	headers := []string{"x-custom-secret", "Authorization", "authorization", " "}
	snap, err := s.Update(context.Background(), Patch{RetentionDays: &days, RedactHeaders: &headers})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if snap.RetentionDays != 30 || !snap.Policy.HeaderRedacted("X-Custom-Secret") {
		t.Fatalf("update not applied: %+v", snap)
	}
	if !slices.Equal(snap.RedactHeaders, []string{"X-Custom-Secret", "Authorization"}) {
		t.Fatalf("headers not normalized/deduplicated: %v", snap.RedactHeaders)
	}
	if store.data[KeyRetentionDays] != "30" {
		t.Fatalf("retention not persisted: %v", store.data)
	}
	if _, ok := store.data[KeyMaxStoredRequests]; ok {
		t.Fatal("unchanged settings should not be stored")
	}

	// A new service over the same store sees the persisted values.
	s2, _, _ := New(context.Background(), store, testDefaults(), 1<<20)
	if s2.Current().RetentionDays != 30 {
		t.Fatal("persisted value not loaded")
	}
}

func TestUpdateRejectsInvalidValuesAtomically(t *testing.T) {
	store := &memStore{data: map[string]string{}}
	s, _, _ := New(context.Background(), store, testDefaults(), 1<<20)

	days := 5
	tooBig := int64(2 << 20)
	bad := []string{"Bad Header"}
	_, err := s.Update(context.Background(), Patch{RetentionDays: &days, DefaultMaxBodyBytes: &tooBig, RedactHeaders: &bad})
	var verr validate.Errors
	if !errors.As(err, &verr) {
		t.Fatalf("expected validation errors, got %v", err)
	}
	if _, ok := verr[KeyDefaultMaxBodyBytes]; !ok {
		t.Errorf("missing body limit error: %v", verr)
	}
	if _, ok := verr[KeyRedactHeaders]; !ok {
		t.Errorf("missing header error: %v", verr)
	}
	if s.Current().RetentionDays != 7 || store.saves != 0 {
		t.Fatal("a failed update must not change anything")
	}
}

func TestResetRestoresDefaults(t *testing.T) {
	store := &memStore{data: map[string]string{}}
	s, _, _ := New(context.Background(), store, testDefaults(), 1<<20)
	days := 1
	if _, err := s.Update(context.Background(), Patch{RetentionDays: &days}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Update(context.Background(), Patch{Reset: []string{KeyRetentionDays}})
	if err != nil {
		t.Fatal(err)
	}
	if snap.RetentionDays != 7 || len(store.data) != 0 {
		t.Fatalf("reset failed: %+v %v", snap, store.data)
	}
	if _, err := s.Update(context.Background(), Patch{Reset: []string{"nope"}}); err == nil {
		t.Fatal("unknown reset key should fail")
	}
}

func TestInvalidStoredValueFallsBackWithWarning(t *testing.T) {
	store := &memStore{data: map[string]string{
		KeyDefaultMaxBodyBytes: "99999999", // above the ceiling passed below
		KeyRetentionDays:       "3",
		KeyRedactFields:        "not json",
	}}
	s, warnings, err := New(context.Background(), store, testDefaults(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v", warnings)
	}
	cur := s.Current()
	if cur.DefaultMaxBodyBytes != 1<<20 || cur.RetentionDays != 3 || !slices.Equal(cur.RedactFields, []string{"password"}) {
		t.Fatalf("unexpected values %+v", cur.Values)
	}
}

func TestEmptyFieldListDisablesBodyRedaction(t *testing.T) {
	s, _, _ := New(context.Background(), &memStore{data: map[string]string{}}, testDefaults(), 1<<20)
	empty := []string{}
	snap, err := s.Update(context.Background(), Patch{RedactFields: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Policy.FieldRedacted("password") {
		t.Fatal("field redaction should be off")
	}
}
