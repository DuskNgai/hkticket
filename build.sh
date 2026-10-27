set -euo pipefail
cd "$(dirname "$0")"

mkdir -p dist

GOOS=darwin  GOARCH=arm64 go build -ldflags="-s -w" -o dist/hkticket-macos-arm64 .
GOOS=darwin  GOARCH=amd64 go build -ldflags="-s -w" -o dist/hkticket-macos-intel .
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o dist/hkticket-windows.exe .
GOOS=linux   GOARCH=amd64 go build -ldflags="-s -w" -o dist/hkticket-linux .

ls -lh dist/
