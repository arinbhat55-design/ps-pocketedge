package store

import "context"

func (s *Store) RequireLocalLogin(ctx context.Context) (bool, error) {
	var required bool
	err := s.pool.QueryRow(ctx, `SELECT require_local_login FROM access_settings WHERE id = TRUE`).Scan(&required)
	return required, err
}

func (s *Store) SetRequireLocalLogin(ctx context.Context, required bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE access_settings SET require_local_login = $1 WHERE id = TRUE`, required)
	return err
}
