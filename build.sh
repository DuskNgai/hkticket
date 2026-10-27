set -euo pipefail
cd "$(dirname "$0")"

mkdir -p dist

GOOS=darwin  GOARCH=arm64 go build -ldflags="-s -w" -o dist/rush-macos-arm64 .
GOOS=darwin  GOARCH=amd64 go build -ldflags="-s -w" -o dist/rush-macos-intel .
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o dist/rush-windows.exe .
GOOS=linux   GOARCH=amd64 go build -ldflags="-s -w" -o dist/rush-linux .

ls -lh dist/
