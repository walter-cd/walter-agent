COMMIT = $$(git describe --always)

deps:
	go get -d -t ./...
	go get github.com/tools/godep

test: deps
	go test ./...
	test `gofmt -l . | wc -l` -eq 0

build: deps
	godep restore
	go build -ldflags "-X main.GitCommit=\"$(COMMIT)\"" -o bin/walter-agent

install: deps
	go install -ldflags "-X main.GitCommit=\"$(COMMIT)\""

clean:
	rm $(GOPATH)/bin/walter-agent
	rm bin/walter-agent
