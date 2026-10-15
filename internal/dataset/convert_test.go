package dataset

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"
)

func TestConvertPreservesIdentifiers(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "tiny.csv")
	body := "vehicleid,chassisno\nbgauss_in_1166,00123\n"
	if err := os.WriteFile(input, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "trips.parquet")
	result, err := Convert(context.Background(), ConvertOptions{
		Input:  input,
		Output: output,
		Header: true,
		Types:  map[string]string{"vehicleid": "varchar", "chassisno": "VARCHAR"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.InputRows != 1 || result.OutputRows != 1 {
		t.Fatalf("rows in %d out %d", result.InputRows, result.OutputRows)
	}
	found := map[string]string{}
	for _, col := range result.Columns {
		found[col.Name] = col.DuckType
	}
	if found["vehicleid"] != "VARCHAR" || found["chassisno"] != "VARCHAR" {
		t.Fatalf("types %#v", found)
	}
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var vehicle, chassis string
	err = db.QueryRow(`SELECT vehicleid, chassisno FROM read_parquet(?)`, output).Scan(&vehicle, &chassis)
	if err != nil {
		t.Fatal(err)
	}
	if vehicle != "bgauss_in_1166" || chassis != "00123" {
		t.Fatalf("values %s %s", vehicle, chassis)
	}
}

func TestConvertEpochMilliseconds(t *testing.T) {
	output := filepath.Join(t.TempDir(), "trips.parquet")
	result, err := Convert(context.Background(), ConvertOptions{
		Input:  filepath.Join("..", "..", "testdata", "trips.csv"),
		Output: output,
		Header: true,
		Types: map[string]string{
			"vehicleid": "VARCHAR",
			"regno":     "VARCHAR",
			"chassisno": "VARCHAR",
			"starttime": "EPOCH_MS",
			"endtime":   "EPOCH_MS",
			"createdat": "EPOCH_MS",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.InputRows < 1000 || result.InputRows != result.OutputRows {
		t.Fatalf("rows in %d out %d", result.InputRows, result.OutputRows)
	}
	byName := map[string]Column{}
	for _, col := range result.Columns {
		byName[col.Name] = col
	}
	for _, name := range []string{"fleetid", "vehicleid", "regno", "chassisno", "starttime", "endtime", "startodo", "endodo", "startsoc", "endsoc", "socconsumed", "distance", "duration", "rangevalue", "createdat"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("missing %s", name)
		}
	}
	if _, ok := byName["region"]; ok {
		t.Fatal("unexpected region column")
	}
	if byName["vehicleid"].DuckType != "VARCHAR" {
		t.Fatalf("vehicleid %s", byName["vehicleid"].DuckType)
	}
	if byName["starttime"].Kind != KindTimestamp {
		t.Fatalf("starttime %s %s", byName["starttime"].DuckType, byName["starttime"].Kind)
	}
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var vehicle string
	var distance float64
	err = db.QueryRow(`SELECT vehicleid, distance FROM read_parquet(?) WHERE vehicleid = 'bgauss_in_1166' AND startodo = 24707.92`, output).Scan(&vehicle, &distance)
	if err != nil {
		t.Fatal(err)
	}
	if vehicle != "bgauss_in_1166" || distance < 5 || distance > 5.2 {
		t.Fatalf("row %s %f", vehicle, distance)
	}
}

func TestConvertRejectsMissingCSVAndSamePath(t *testing.T) {
	if _, err := Convert(context.Background(), ConvertOptions{Input: filepath.Join(t.TempDir(), "missing.csv"), Output: filepath.Join(t.TempDir(), "out.parquet"), Header: true}); err == nil {
		t.Fatal("expected missing csv error")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(path, []byte("a\n1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Convert(context.Background(), ConvertOptions{Input: path, Output: path, Header: true}); err == nil {
		t.Fatal("expected refusal to overwrite csv")
	}
}

func TestTypeOverrideRejectsUnknownType(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(input, []byte("vehicleid\n001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Convert(context.Background(), ConvertOptions{
		Input: input, Output: filepath.Join(dir, "out.parquet"), Header: true,
		Types: map[string]string{"vehicleid": "BLOB"},
	})
	if err == nil {
		t.Fatal("expected unsupported type")
	}
}

func TestRenameAndStrptime(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "alerts.csv")
	body := "regno,alert time\nKA01,12 Oct 26 | 18:41:53\n"
	if err := os.WriteFile(input, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "geofence.parquet")
	result, err := Convert(context.Background(), ConvertOptions{
		Input: input, Output: output, Header: true,
		Rename: map[string]string{"alert time": "alert_time"},
		Types:  map[string]string{"regno": "VARCHAR", "alert_time": "STRPTIME:%d %b %y | %H:%M:%S"},
	})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, col := range result.Columns {
		found[col.Name] = col.DuckType
	}
	if _, ok := found["alert time"]; ok {
		t.Fatalf("original header kept %#v", found)
	}
	if found["alert_time"] != "TIMESTAMP" {
		t.Fatalf("types %#v", found)
	}
}
