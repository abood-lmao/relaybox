package storage

import "context"

// LoadSettings returns all stored runtime settings as raw JSON values.
func (db *DB) LoadSettings(ctx context.Context) (map[string]string, error) {
	rows, err := db.r.QueryContext(ctx, "SELECT key, value FROM settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SaveSettings upserts values and deletes the keys in remove, atomically.
func (db *DB) SaveSettings(ctx context.Context, values map[string]string, remove []string) error {
	tx, err := db.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := millis(db.now())
	for k, v := range values {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			k, v, now); err != nil {
			return err
		}
	}
	for _, k := range remove {
		if _, err := tx.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", k); err != nil {
			return err
		}
	}
	return tx.Commit()
}
