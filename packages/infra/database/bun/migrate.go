package bun

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bunlib "github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
	"github.com/urfave/cli/v2"

	"modular/packages/infra/database"
)

// MigrationTool provides Bun SQL migrations plus model-based table creation.
type MigrationTool struct {
	db           *bunlib.DB
	models       []interface{}
	migrations   *migrate.Migrations
	migrationsFS embed.FS
	outputDir    string
}

// NewMigrationTool creates a migration tool from an explicit Bun database.
func NewMigrationTool(db *bunlib.DB, migrationsFS embed.FS) *MigrationTool {
	return &MigrationTool{
		db:           db,
		models:       make([]interface{}, 0),
		migrations:   migrate.NewMigrations(),
		migrationsFS: migrationsFS,
		outputDir:    filepath.Join("cmd", "migrate", "migrations"),
	}
}

// NewBunMigration creates a migration tool using the package-level Bun connection.
func NewBunMigration(migrationsFS embed.FS) *MigrationTool {
	return NewMigrationTool(GetBunDB(), migrationsFS)
}

// WithOutputDir changes where the create command writes SQL migration files.
func (t *MigrationTool) WithOutputDir(dir string) *MigrationTool {
	if dir != "" {
		t.outputDir = dir
	}
	return t
}

// RegisterModels registers models for create-tables and drop-tables commands.
func (t *MigrationTool) RegisterModels(models ...interface{}) {
	t.models = append(t.models, models...)
}

// Run runs the migration CLI.
func (t *MigrationTool) Run() error {
	if t.db == nil {
		return fmt.Errorf("database connection is not initialized, call NewBunConnection first or use NewMigrationTool")
	}

	app := &cli.App{
		Name:  "migrate",
		Usage: "Database migration tool",
		Commands: []*cli.Command{
			{
				Name:  "init",
				Usage: "Initialize migration table",
				Action: func(c *cli.Context) error {
					return t.initMigrations(c.Context)
				},
			},
			{
				Name:  "up",
				Usage: "Run migrations",
				Action: func(c *cli.Context) error {
					return t.runMigrations(c.Context)
				},
			},
			{
				Name:  "down",
				Usage: "Rollback migrations",
				Action: func(c *cli.Context) error {
					return t.rollbackMigrations(c.Context)
				},
			},
			{
				Name:  "status",
				Usage: "Show migration status",
				Action: func(c *cli.Context) error {
					return t.showMigrationStatus(c.Context)
				},
			},
			{
				Name:  "create",
				Usage: "Create a new migration",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "name",
						Aliases:  []string{"n"},
						Usage:    "Migration name",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					return t.createMigration(c.String("name"))
				},
			},
			{
				Name:  "create-tables",
				Usage: "Create tables from registered models",
				Action: func(c *cli.Context) error {
					return t.createTablesFromModels(c.Context)
				},
			},
			{
				Name:  "drop-tables",
				Usage: "Drop all registered tables",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "yes",
						Usage: "Confirm dangerous operation",
					},
				},
				Action: func(c *cli.Context) error {
					if !c.Bool("yes") {
						return fmt.Errorf("this is a dangerous operation, use --yes flag to confirm")
					}
					return t.dropTablesFromModels(c.Context)
				},
			},
		},
	}

	return app.Run(os.Args)
}

func (t *MigrationTool) initMigrations(ctx context.Context) error {
	if err := t.loadSQLMigrations(); err != nil {
		return err
	}

	migrator := migrate.NewMigrator(t.db, t.migrations)
	if err := migrator.Init(ctx); err != nil {
		return fmt.Errorf("failed to init migrations: %w", err)
	}

	fmt.Println("Migration table initialized successfully")
	return nil
}

func (t *MigrationTool) runMigrations(ctx context.Context) error {
	if err := t.loadSQLMigrations(); err != nil {
		return err
	}

	migrator := migrate.NewMigrator(t.db, t.migrations)
	if err := migrator.Init(ctx); err != nil {
		fmt.Println("Migration table already exists")
	} else {
		fmt.Println("Migration table initialized")
	}

	group, err := migrator.Migrate(ctx)
	if err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}
	if group.ID == 0 {
		fmt.Println("No new migrations to run")
		return nil
	}

	fmt.Printf("Migrated to version: %d (%d migrations)\n", group.ID, len(group.Migrations))
	return nil
}

func (t *MigrationTool) rollbackMigrations(ctx context.Context) error {
	if err := t.loadSQLMigrations(); err != nil {
		return err
	}

	migrator := migrate.NewMigrator(t.db, t.migrations)
	if err := migrator.Init(ctx); err != nil {
		fmt.Println("Migration table already exists")
	}

	group, err := migrator.Rollback(ctx)
	if err != nil {
		return fmt.Errorf("failed to rollback migrations: %w", err)
	}
	if group.ID == 0 {
		fmt.Println("No migrations to rollback")
		return nil
	}

	fmt.Printf("Rolled back %d migrations\n", len(group.Migrations))
	return nil
}

func (t *MigrationTool) showMigrationStatus(ctx context.Context) error {
	if err := t.loadSQLMigrations(); err != nil {
		return err
	}

	migrator := migrate.NewMigrator(t.db, t.migrations)
	if err := migrator.Init(ctx); err != nil {
		fmt.Println("Migration table already exists")
	}

	status, err := migrator.MigrationsWithStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to get migration status: %w", err)
	}

	fmt.Println("Migration Status:")
	fmt.Println(status)
	return nil
}

func (t *MigrationTool) createMigration(name string) error {
	if err := os.MkdirAll(t.outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create migration directory: %w", err)
	}

	version := time.Now().Format("20060102150405")
	upFile := filepath.Join(t.outputDir, fmt.Sprintf("%s_%s.up.sql", version, name))
	downFile := filepath.Join(t.outputDir, fmt.Sprintf("%s_%s.down.sql", version, name))

	upContent := fmt.Sprintf("-- Migration: %s\n-- Created at: %s\n\n", name, time.Now().Format(time.RFC3339))
	if err := os.WriteFile(upFile, []byte(upContent), 0644); err != nil {
		return fmt.Errorf("failed to create up migration file: %w", err)
	}

	downContent := fmt.Sprintf("-- Rollback: %s\n-- Created at: %s\n\n", name, time.Now().Format(time.RFC3339))
	if err := os.WriteFile(downFile, []byte(downContent), 0644); err != nil {
		return fmt.Errorf("failed to create down migration file: %w", err)
	}

	fmt.Printf("Created migration files:\n")
	fmt.Printf("  - %s\n", upFile)
	fmt.Printf("  - %s\n", downFile)
	return nil
}

func (t *MigrationTool) createTablesFromModels(ctx context.Context) error {
	if len(t.models) == 0 {
		return fmt.Errorf("no models registered, call RegisterModels first")
	}

	fmt.Printf("Creating tables for %d models...\n", len(t.models))
	for _, model := range t.models {
		if _, err := t.db.NewCreateTable().Model(model).IfNotExists().Exec(ctx); err != nil {
			return fmt.Errorf("failed to create table for %T: %w", model, err)
		}
		fmt.Printf("Created table for: %T\n", model)

		if indexer, ok := model.(database.ModelIndexer); ok {
			if err := t.createIndexesForModel(ctx, model, indexer.DefineIndexes()); err != nil {
				return fmt.Errorf("failed to create indexes for %T: %w", model, err)
			}
		}
	}

	fmt.Println("All tables created successfully")
	return nil
}

func (t *MigrationTool) createIndexesForModel(ctx context.Context, model interface{}, indexes []database.IndexDefinition) error {
	if len(indexes) == 0 {
		return nil
	}

	tableName, err := t.getTableName(model)
	if err != nil {
		return err
	}

	for _, idx := range indexes {
		unique := ""
		if idx.Unique {
			unique = "UNIQUE "
		}

		columns := ""
		for i, col := range idx.Columns {
			if i > 0 {
				columns += ", "
			}
			columns += col
		}

		query := fmt.Sprintf("CREATE %sINDEX IF NOT EXISTS %s ON %s (%s)", unique, idx.Name, tableName, columns)
		if _, err := t.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to create index %s: %w", idx.Name, err)
		}
		fmt.Printf("Created index: %s\n", idx.Name)
	}

	return nil
}

func (t *MigrationTool) getTableName(model interface{}) (string, error) {
	query := t.db.NewSelect().Model(model)
	tableName := query.GetTableName()
	if tableName == "" {
		return "", fmt.Errorf("failed to get table name for %T", model)
	}
	return tableName, nil
}

func (t *MigrationTool) dropTablesFromModels(ctx context.Context) error {
	if len(t.models) == 0 {
		return fmt.Errorf("no models registered")
	}

	fmt.Printf("Dropping tables for %d models...\n", len(t.models))
	for i := len(t.models) - 1; i >= 0; i-- {
		model := t.models[i]
		if _, err := t.db.NewDropTable().Model(model).IfExists().Exec(ctx); err != nil {
			return fmt.Errorf("failed to drop table for %T: %w", model, err)
		}
		fmt.Printf("Dropped table for: %T\n", model)
	}

	fmt.Println("All tables dropped successfully")
	return nil
}

func (t *MigrationTool) loadSQLMigrations() error {
	if err := t.migrations.Discover(t.migrationsFS); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to discover migrations: %w", err)
	}
	return nil
}
