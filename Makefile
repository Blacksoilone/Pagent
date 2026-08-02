.PHONY: build init chat serve frontend test

build:
	go build -o pagent ./cmd/pagent

test:
	go test ./...

init: build
	./pagent init --dir ~/.pagent

chat: build
	./pagent chat --dir ~/.pagent "$(MSG)"

serve: build
	./pagent serve --dir ~/.pagent --base-url $(BASE_URL) --model $(MODEL)

frontend:
	cd frontend && npm run dev
