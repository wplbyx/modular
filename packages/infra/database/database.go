package database

// Dialect constants identify supported SQL database dialects at composition time.
const (
	DialectSQLite     = "sqlite"
	DialectMySQL      = "mysql"
	DialectPostgres   = "postgres"
	DialectClickHouse = "clickhouse"
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
