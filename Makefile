.PHONY: fmt vet test convert convert-datasets run run-client docker

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

convert: convert-datasets
	go run ./scripts/convert_csv.go -input testdata/trips.csv -output data/parquet/trips.parquet -types vehicleid:VARCHAR,regno:VARCHAR,chassisno:VARCHAR,starttime:EPOCH_MS,endtime:EPOCH_MS,createdat:EPOCH_MS

convert-datasets:
	go run ./scripts/convert_csv.go -input testdata/charges.csv -output data/parquet/charge.parquet -types vehicleid:VARCHAR,regno:VARCHAR,chassisno:VARCHAR,starttime:EPOCH_MS,endtime:EPOCH_MS,createdat:EPOCH_MS
	go run ./scripts/convert_csv.go -input testdata/trips.csv -output data/parquet/distance.parquet -types vehicleid:VARCHAR,regno:VARCHAR,chassisno:VARCHAR,starttime:EPOCH_MS,endtime:EPOCH_MS,createdat:EPOCH_MS
	go run ./scripts/convert_csv.go -input testdata/geofences.csv -output data/parquet/geofence.parquet -rename "alert time:alert_time" -types "regno:VARCHAR,alert_time:STRPTIME:%d %b %y | %H:%M:%S"
	go run ./scripts/convert_csv.go -input testdata/alerts.csv -output data/parquet/alert_report.parquet -rename "alert time:alert_time" -types "regno:VARCHAR,chassisno:VARCHAR,deviceid:VARCHAR,alert_time:STRPTIME:%d %b %y | %H:%M:%S"

run:
	go run ./cmd/server

run-client:
	go run ./cmd/client

docker:
	docker build -t analytics-mcp .
