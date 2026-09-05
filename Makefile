.PHONY: build test eval fmt lint sync ci clean

build:
	go build -o finterminal .

test:
	go test ./...

eval: build
	./finterminal eval

sync: build
	./finterminal sync

fmt:
	gofmt -w .

lint:
	go vet ./...

ci: fmt lint test eval

clean:
	rm -f finterminal finterminal.exe
