package database

// DSN constants identify supported database dialects.
const (
	DSNSqlite     = "sqlite"
	DSNMySQL      = "mysql"
	DSNPostgres   = "postgres"
	DSNClickhouse = "clickhouse"
)

// IndexDefinition represents a database index for migration tooling.
type IndexDefinition struct {
	Name    string
	Columns []string
	Unique  bool
}

// ModelIndexer is implemented by models that declare indexes for migration tooling.
type ModelIndexer interface {
	DefineIndexes() []IndexDefinition
}
