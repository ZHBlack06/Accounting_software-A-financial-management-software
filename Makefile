# Windows(MSYS/Git Bash) 或 Linux/macOS 均可；Windows 建议直接用 build.ps1
PROFILE ?= v1.2

.PHONY: all web server desktop installer test clean

all: web server desktop installer

web:
	cd web && npm ci && npm run build

server:
	CGO_ENABLED=0 go build -trimpath -o ledger.exe ./cmd/server

desktop:
	CGO_ENABLED=0 go build -trimpath -ldflags "-H windowsgui -s -w" -o ledger-desktop.exe ./cmd/desktop

installer:
	cd installer-kit && go run ./build -profile $(PROFILE) -src ..

test:
	go test ./... -count=1
	cd web && npm run test --if-present

clean:
	rm -f ledger.exe ledger-desktop.exe
	rm -rf web/dist
