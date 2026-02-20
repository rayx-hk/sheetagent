package sheet

type SpreadsheetOverview struct {
	FilePath   string      `json:"file_path"`
	FileSize   int64       `json:"file_size"`
	SheetNames []string    `json:"sheet_names"`
	Sheets     []SheetInfo `json:"sheets"`
	TotalCells int         `json:"total_cells"`
	Compressed string      `json:"compressed"`
}

type SheetInfo struct {
	Name        string            `json:"name"`
	ActiveRange string            `json:"active_range"`
	Headers     []string          `json:"headers"`
	RowCount    int               `json:"row_count"`
	ColCount    int               `json:"col_count"`
	MergedCells []string          `json:"merged_cells"`
	DataTypes   map[string]string `json:"data_types"`
	SampleRows  [][]string        `json:"sample_rows"`
}
