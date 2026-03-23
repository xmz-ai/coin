package db

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

func AutoMigrate(ctx context.Context, pool *pgxpool.Pool, migrations fs.FS) error {
	entries, err := fs.Glob(migrations, "*.up.sql")
	if err != nil {
		return fmt.Errorf("glob migrations: %w", err)
	}
	sort.Strings(entries)
	for _, name := range entries {
		data, err := fs.ReadFile(migrations, name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		log.Printf("[migrate] applying %s", name)
		if _, err := pool.Exec(ctx, string(data)); err != nil {
			return fmt.Errorf("exec %s: %w", name, err)
		}
	}
	log.Printf("[migrate] done (%d files)", len(entries))
	return nil
}
