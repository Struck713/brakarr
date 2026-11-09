package database

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path"
)

const MIGRATIONS_FOLDER = "./migrations"

func migrate(db *sql.DB) error {

	migratonFiles, err := os.ReadDir(MIGRATIONS_FOLDER)
	if err != nil {
		return err
	}

	var migrations []string
	for _, migrationFile := range migratonFiles {
		absolutePath := path.Join(MIGRATIONS_FOLDER, migrationFile.Name())
		content, err := os.ReadFile(absolutePath)
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
