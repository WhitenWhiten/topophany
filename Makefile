.PHONY: build test race vet fmt fmt-check lint cover verify banner site site-check captures device-shots icon clean
build:
	mkdir -p bin
	go build -trimpath $(if $(TAGS),-tags $(TAGS)) -o bin/topo ./cmd/topo
test:
	go test -count=1 ./...
race:
	go test -race -count=1 ./...
vet:
	go vet ./...
fmt:
	gofmt -w cmd internal
fmt-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
lint: fmt-check vet
cover:
	go test -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1
verify:
	python3 scripts/verify.py
banner:
	TOPO_BANNER=$(CURDIR)/docs/assets/banner.png go test -count=1 -run '^TestBanner$$' ./internal/tui
site:
	TOPO_SITE=$(CURDIR)/site/public/data go test -count=1 -run '^TestSite$$' ./internal/tui
	python3 scripts/build_demo.py
	python3 scripts/build_site.py
# Rebuild and fail when the result is not what is committed: the pages and the
# recorded tours are generated, and a stale one makes every later build churn.
site-check: site
	@test -z "$$(git status --porcelain --untracked-files=all -- site/)" || { echo "site/ is stale: commit the regenerated pages and tours"; git status --short --untracked-files=all -- site/; exit 1; }
captures:
	python3 scripts/capture.py $(if $(FONTS),--fonts $(FONTS))
device-shots:
	python3 scripts/device_shots.py $(if $(DUO_KIT),--duo-kit $(DUO_KIT))
icon:
	python3 scripts/icon.py
clean:
	rm -rf bin dist .verify-build .device-shots coverage.out

# Native Web UI is optional; ordinary builds never need Node or frontend tools.
.PHONY: build-web web-test web-browser
build-web:
	mkdir -p bin
	go build -trimpath -tags webui -o bin/topo-web ./cmd/topo
web-test:
	go test -tags webui ./...
	node --test internal/webui/tests/*.test.mjs
web-browser: build-web
	node internal/webui/tests/browser.mjs
