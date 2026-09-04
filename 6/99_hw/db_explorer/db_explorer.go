package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

var (
	ErrUnknownTable   = errors.New("unknown table")
	ErrUnknownMethod  = errors.New("unknown method")
	ErrRecordNotFound = errors.New("record not found")
	ErrInvalidType    = errors.New("invalid type")
	ErrUnpackJSON     = errors.New("unpack json error")
)

func invalidField(name string) error {
	return fmt.Errorf("field %s have %w", name, ErrInvalidType)
}

type column struct {
	Name     string
	Kind     string
	Nullable bool
	Auto     bool
	Primary  bool
}

type table struct {
	Name    string
	Columns []column
	PK      column
}

type dbExplorer struct {
	db     *sql.DB
	tables map[string]table
	names  []string
}

func NewDbExplorer(db *sql.DB) (http.Handler, error) {
	names, err := showTables(db)
	if err != nil {
		return nil, err
	}

	tables := make(map[string]table, len(names))
	for _, name := range names {
		t, err := showColumns(db, name)
		if err != nil {
			return nil, err
		}
		tables[name] = t
	}

	return &dbExplorer{db: db, tables: tables, names: names}, nil
}

func showTables(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SHOW TABLES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func showColumns(db *sql.DB, name string) (table, error) {
	rows, err := db.Query("SHOW COLUMNS FROM " + quoteIdent(name))
	if err != nil {
		return table{}, err
	}
	defer rows.Close()

	t := table{Name: name}
	for rows.Next() {
		var field, dbType, null, key, extra string
		var unusedDefault sql.NullString
		if err := rows.Scan(&field, &dbType, &null, &key, &unusedDefault, &extra); err != nil {
			return table{}, err
		}
		c := column{
			Name:     field,
			Kind:     kindOf(dbType),
			Nullable: null == "YES",
			Auto:     strings.Contains(extra, "auto_increment"),
			Primary:  key == "PRI",
		}
		t.Columns = append(t.Columns, c)
		if c.Primary {
			t.PK = c
		}
	}
	return t, rows.Err()
}

func kindOf(dbType string) string {
	switch {
	case strings.Contains(dbType, "int"):
		return "int"
	case strings.Contains(dbType, "float"), strings.Contains(dbType, "double"),
		strings.Contains(dbType, "decimal"):
		return "float"
	default:
		return "string"
	}
}

func (e *dbExplorer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

	if parts[0] == "" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusNotFound, ErrUnknownMethod)
			return
		}
		writeResponse(w, map[string]interface{}{"tables": e.names})
		return
	}

	if len(parts) > 2 {
		writeError(w, http.StatusNotFound, ErrUnknownMethod)
		return
	}

	t, ok := e.tables[parts[0]]
	if !ok {
		writeError(w, http.StatusNotFound, ErrUnknownTable)
		return
	}

	id := ""
	if len(parts) == 2 {
		id = parts[1]
	}

	switch {
	case id == "" && r.Method == http.MethodGet:
		e.list(w, r, t)
	case id == "" && r.Method == http.MethodPut:
		e.create(w, r, t)
	case id != "" && r.Method == http.MethodGet:
		e.get(w, t, id)
	case id != "" && r.Method == http.MethodPost:
		e.update(w, r, t, id)
	case id != "" && r.Method == http.MethodDelete:
		e.delete(w, t, id)
	default:
		writeError(w, http.StatusNotFound, ErrUnknownMethod)
	}
}

func (e *dbExplorer) list(w http.ResponseWriter, r *http.Request, t table) {
	params := r.URL.Query()
	limit := intParam(params.Get("limit"), 5)
	offset := intParam(params.Get("offset"), 0)

	query := "SELECT * FROM " + quoteIdent(t.Name) +
		" ORDER BY " + quoteIdent(t.PK.Name) + " LIMIT ? OFFSET ?"

	rows, err := e.db.Query(query, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	records, err := scanRecords(rows, t)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeResponse(w, map[string]interface{}{"records": records})
}

func (e *dbExplorer) get(w http.ResponseWriter, t table, id string) {
	query := "SELECT * FROM " + quoteIdent(t.Name) +
		" WHERE " + quoteIdent(t.PK.Name) + " = ?"

	rows, err := e.db.Query(query, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	records, err := scanRecords(rows, t)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(records) == 0 {
		writeError(w, http.StatusNotFound, ErrRecordNotFound)
		return
	}

	writeResponse(w, map[string]interface{}{"record": records[0]})
}

func (e *dbExplorer) create(w http.ResponseWriter, r *http.Request, t table) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}

	names := []string{}
	holders := []string{}
	values := []interface{}{}
	var pk interface{}

	for _, c := range t.Columns {
		if c.Primary && c.Auto {
			continue
		}

		value := zeroValue(c)
		if raw, exists := body[c.Name]; exists {
			converted, ok := convertInput(c, raw)
			if !ok {
				writeError(w, http.StatusBadRequest, invalidField(c.Name))
				return
			}
			value = converted
		}

		if c.Primary {
			pk = value
		}
		names = append(names, quoteIdent(c.Name))
		holders = append(holders, "?")
		values = append(values, value)
	}

	query := "INSERT INTO " + quoteIdent(t.Name) +
		" (" + strings.Join(names, ", ") + ") VALUES (" + strings.Join(holders, ", ") + ")"

	res, err := e.db.Exec(query, values...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if t.PK.Auto {
		pk, err = res.LastInsertId()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	writeResponse(w, map[string]interface{}{t.PK.Name: pk})
}

func (e *dbExplorer) update(w http.ResponseWriter, r *http.Request, t table, id string) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}

	sets := []string{}
	values := []interface{}{}

	for _, c := range t.Columns {
		raw, exists := body[c.Name]
		if !exists {
			continue
		}
		if c.Primary {
			writeError(w, http.StatusBadRequest, invalidField(c.Name))
			return
		}
		converted, ok := convertInput(c, raw)
		if !ok {
			writeError(w, http.StatusBadRequest, invalidField(c.Name))
			return
		}
		sets = append(sets, quoteIdent(c.Name)+" = ?")
		values = append(values, converted)
	}

	if len(sets) == 0 {
		writeResponse(w, map[string]interface{}{"updated": 0})
		return
	}

	query := "UPDATE " + quoteIdent(t.Name) + " SET " + strings.Join(sets, ", ") +
		" WHERE " + quoteIdent(t.PK.Name) + " = ?"
	values = append(values, id)

	res, err := e.db.Exec(query, values...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	updated, err := res.RowsAffected()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeResponse(w, map[string]interface{}{"updated": updated})
}

func (e *dbExplorer) delete(w http.ResponseWriter, t table, id string) {
	query := "DELETE FROM " + quoteIdent(t.Name) +
		" WHERE " + quoteIdent(t.PK.Name) + " = ?"

	res, err := e.db.Exec(query, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeResponse(w, map[string]interface{}{"deleted": deleted})
}

func scanRecords(rows *sql.Rows, t table) ([]map[string]interface{}, error) {
	values := make([]interface{}, len(t.Columns))
	pointers := make([]interface{}, len(t.Columns))
	for i := range values {
		pointers[i] = &values[i]
	}

	records := []map[string]interface{}{}
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		record := map[string]interface{}{}
		for i, c := range t.Columns {
			if text, ok := values[i].([]byte); ok {
				record[c.Name] = string(text)
			} else {
				record[c.Name] = values[i]
			}
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func convertInput(c column, value interface{}) (interface{}, bool) {
	if value == nil {
		return nil, c.Nullable
	}
	switch c.Kind {
	case "int":
		number, ok := value.(float64)
		return int(number), ok
	case "float":
		number, ok := value.(float64)
		return number, ok
	default:
		text, ok := value.(string)
		return text, ok
	}
}

func zeroValue(c column) interface{} {
	if c.Nullable {
		return nil
	}
	if c.Kind == "string" {
		return ""
	}
	return 0
}

func decodeBody(w http.ResponseWriter, r *http.Request) (map[string]interface{}, bool) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, ErrUnpackJSON)
		return nil, false
	}
	return body, true
}

func intParam(raw string, def int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return def
	}
	return n
}

func quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeResponse(w http.ResponseWriter, data interface{}) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"response": data})
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]interface{}{"error": err.Error()})
}
