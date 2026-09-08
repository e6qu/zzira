package models

type WikiDatabaseColumn struct {
	ID, DatabaseID, Key, Name, Type string
	Options                         []string
	Position                        int
}

type WikiDatabaseRow struct {
	ID, DatabaseID, CreatedBy, CreatedAt, UpdatedAt string
	Values                                          map[string]string
}

type WikiDatabaseView struct {
	ID, DatabaseID, Name, SortKey, SortDirection, FilterKey, FilterValue string
}

type WikiDatabaseData struct {
	Columns []WikiDatabaseColumn
	Rows    []WikiDatabaseRow
	Views   []WikiDatabaseView
}
