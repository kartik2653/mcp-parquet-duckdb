.PHONY: fmt vet test convert run docker

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

convert:
	go run ./scripts/convert_csv.go -input testdata/trips.csv -output data/parquet/trips.parquet -types vehicleid:VARCHAR,regno:VARCHAR,chassisno:VARCHAR,starttime:EPOCH_MS,endtime:EPOCH_MS,createdat:EPOCH_MS

run:
	go run ./cmd/server

docker:
	docker build -t analytics-mcp .
