#!/bin/bash
set -e

INSTALL_DIR="$HOME/.local/bin"
PID_FILE="$HOME/.shepherd/shepherd.pid"
BIN="$INSTALL_DIR/shepherd"
PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"

cd "$PROJECT_DIR"

# 1. 데몬 중지
echo "🛑 데몬 중지 중..."
if [ -f "$PID_FILE" ]; then
    PID=$(cat "$PID_FILE")
    if kill -0 "$PID" 2>/dev/null; then
        kill "$PID"
        # 프로세스 종료 대기 (최대 5초)
        for i in $(seq 1 50); do
            if ! kill -0 "$PID" 2>/dev/null; then
                break
            fi
            sleep 0.1
        done
        # 아직 살아있으면 강제 종료
        if kill -0 "$PID" 2>/dev/null; then
            kill -9 "$PID" 2>/dev/null || true
            sleep 0.5
        fi
        echo "   데몬 중지됨 (PID: $PID)"
    else
        echo "   PID $PID 프로세스 없음 (이미 종료됨)"
    fi
    rm -f "$PID_FILE"
else
    echo "   PID 파일 없음 (데몬 미실행)"
fi

# 2. Svelte 웹 빌드
echo ""
echo "🌐 Svelte 웹 빌드 중..."
cd "$PROJECT_DIR/web"
npm run build
echo "   웹 빌드 완료"

# 3. web_dist 복사
echo ""
echo "📁 web_dist 동기화 중..."
rm -rf "$PROJECT_DIR/internal/server/web_dist"
cp -r "$PROJECT_DIR/web/build" "$PROJECT_DIR/internal/server/web_dist"
echo "   web_dist 동기화 완료"

# 4. Go 빌드
echo ""
echo "🔨 Go 빌드 중..."
cd "$PROJECT_DIR"
go build -o shepherd ./cmd/shepherd
echo "   Go 빌드 완료"

# 5. 설치
echo ""
echo "📦 설치 중..."
mkdir -p "$INSTALL_DIR"
rm -f "$BIN"
cp "$PROJECT_DIR/shepherd" "$BIN"
rm -f "$PROJECT_DIR/shepherd"
echo "   $BIN 설치 완료"

# 6. 데몬 재시작
echo ""
echo "🚀 데몬 시작 중..."
env -u CLAUDECODE "$BIN" serve -d
sleep 1

if [ -f "$PID_FILE" ]; then
    NEW_PID=$(cat "$PID_FILE")
    echo "   데몬 시작됨 (PID: $NEW_PID)"
else
    echo "   ⚠️  PID 파일이 생성되지 않음 - 수동 확인 필요"
fi

echo ""
echo "✅ 설치 완료!"
