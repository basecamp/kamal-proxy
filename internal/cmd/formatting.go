package cmd

import (
	"fmt"
	"strings"
)

type Table struct {
	ColumnWidths map[int]int
	Rows         [][]string
}

func NewTable() *Table {
	return &Table{
		ColumnWidths: map[int]int{},
		Rows:         [][]string{},
	}
}

func (t *Table) AddRow(row []string) {
	t.updateColumnWidths(row)
	t.Rows = append(t.Rows, row)
}

func (t *Table) Print() {
	for rownum, row := range t.Rows {
		for i, cell := range row {
			cellStyle := plain
			if rownum == 0 {
				cellStyle = italic
			}
			if rownum > 0 && i == 0 {
				cellStyle = bold
			}

			pad := t.ColumnWidths[i] - len(cell)
			fmt.Printf("%s%s  ", cellStyle.format(cell), strings.Repeat(" ", pad))
		}
		fmt.Println()
	}
}

// Private

type style string

const (
	plain  style = ""
	bold   style = "1;34"
	italic style = "3;94"
)

func (s style) format(value string) string {
	return "\033[" + string(s) + "m" + value + "\033[0m"
}

func (t *Table) updateColumnWidths(row []string) {
	for i, cell := range row {
		if len(cell) > t.ColumnWidths[i] {
			t.ColumnWidths[i] = len(cell)
		}
	}
}
