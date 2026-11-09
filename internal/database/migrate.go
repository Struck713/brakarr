package database

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func migrate(db *sql.DB) error {
	entries, err := fs.ReadDir(migrationFS, "migrations") // sorted by filename
	if err != nil {
		return err
	}
	var migrations []string
	for _, entry := range entries {
		content, err := migrationFS.ReadFile(path.Join("migrations", entry.Name()))
		if err != nil {
			return err
		}
		migrations = append(migrations, string(content))
	}

	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}

	slog.Info("running migrations", "old_version", version, "new_version", len(migrations))

	if version > len(migrations) {
		return fmt.Errorf("database schema v%d is newer than this build (v%d)", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		slog.Info("ran migration", "sql", migrations[i])
	}
	return nil
}
