#!/bin/bash
set -e

# 설치 대상 디렉터리: Makefile의 install과 같은 경로로 맞춘다.
# 이 경로는 사용자의 PATH 앞에 두어야 한다(README 참고). 그래야 터미널과
# systemd 유닛이 모두 방금 설치한 바이너리를 찾는다.
INSTALL_DIR="$HOME/.local/bin"
PID_FILE="$HOME/.shepherd/shepherd.pid"
BIN="$INSTALL_DIR/shepherd"
PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"

cd "$PROJECT_DIR"

# 실행 중인 데몬을 관리하는 방식을 감지한다. 우선순위는 다음과 같다:
#   1) systemd 사용자 유닛   2) systemd 시스템 유닛(쿼리만 sudo 불필요)
#   3) PID 파일             4) pgrep 폴백(유닛·PID 없이 실행된 경우)
detect_manager() {
    if systemctl --user is-active --quiet shepherd 2>/dev/null; then
        echo "user"
    elif systemctl is-active --quiet shepherd 2>/dev/null; then
        echo "system"
    elif [ -f "$PID_FILE" ]; then
        # PID 파일이 있어도 프로세스가 죽었을 수 있으므로 실제 생존을 확인한다.
        local pid
        pid="$(cat "$PID_FILE" 2>/dev/null || echo)"
        if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
            echo "pid"
        else
            echo ""
        fi
    elif pgrep -f '[s]hepherd serve' >/dev/null 2>&1; then
        echo "pgrep"
    else
        echo ""
    fi
}

# 데몬이 실제로 멈출 때까지 최대 30초 대기한다. SIGTERM 후 데몬은 PID 파일을
# 정리하며 정상 종료하므로, 이 대기가 끝나면 바이너리 교체가 안전하다.
wait_for_stop() {
    local waited=0
    while [ "$waited" -lt 30 ]; do
        if ! pgrep -f '[s]hepherd serve' >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done
    echo "   ⚠️  데몬이 30초 안에 멈추지 않았습니다. 진행 중 작업이 있는지 확인하십시오."
    return 1
}

# 1. 데몬 중지 (감지한 방식으로만 중지한다. PID 파일 유무와 무관하게 실행 중이면 중지한다.)
echo "🛑 데몬 중지 중..."
MANAGE="$(detect_manager)"
WAS_RUNNING=0
case "$MANAGE" in
    user)
        WAS_RUNNING=1
        echo "   systemd 사용자 유닛으로 실행 중 → systemctl --user stop shepherd"
        systemctl --user stop shepherd || true
        wait_for_stop || true
        ;;
    system)
        WAS_RUNNING=1
        echo "   systemd 시스템 유닛으로 실행 중 → sudo systemctl stop shepherd"
        sudo systemctl stop shepherd || true
        wait_for_stop || true
        ;;
    pid)
        WAS_RUNNING=1
        PID="$(cat "$PID_FILE")"
        echo "   PID 파일($PID)으로 실행 중 → SIGTERM $PID"
        kill "$PID" 2>/dev/null || true
        wait_for_stop || true
        rm -f "$PID_FILE"
        ;;
    pgrep)
        WAS_RUNNING=1
        echo "   systemd 유닛·PID 파일 없이 실행 중 → pkill shepherd serve"
        pkill -TERM -f '[s]hepherd serve' || true
        wait_for_stop || true
        ;;
    *)
        echo "   실행 중인 데몬이 없습니다."
        ;;
esac

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

# 5. 설치 (Makefile과 같은 INSTALL_DIR에 설치한다.)
echo ""
echo "📦 설치 중..."
mkdir -p "$INSTALL_DIR"
rm -f "$BIN"
cp "$PROJECT_DIR/shepherd" "$BIN"
rm -f "$PROJECT_DIR/shepherd"
echo "   $BIN 설치 완료"

# PATH 확인: 이 디렉터리가 PATH에 없으면 터미널과 systemd가 새 바이너리를 찾지 못한다.
case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
        echo "   ⚠️  $INSTALL_DIR 가 PATH에 없습니다. 셸 설정에 다음을 추가하십시오:"
        echo "       export PATH=\"$INSTALL_DIR:\$PATH\""
        ;;
esac

# 6. 데몬 재시작 (중지할 때 사용한 방식과 같은 방식으로 되돌린다.)
echo ""
echo "🚀 데몬 시작 중..."
case "$MANAGE" in
    user)
        systemctl --user start shepherd || true
        sleep 1
        systemctl --user is-active --quiet shepherd \
            && echo "   데몬 재시작됨 (systemd 사용자 유닛)" \
            || echo "   ⚠️  데몬이 시작되지 않았습니다. journalctl --user -u shepherd 를 확인하십시오."
        ;;
    system)
        sudo systemctl start shepherd || true
        sleep 1
        systemctl is-active --quiet shepherd \
            && echo "   데몬 재시작됨 (systemd 시스템 유닛)" \
            || echo "   ⚠️  데몬이 시작되지 않았습니다. journalctl -u shepherd 를 확인하십시오."
        ;;
    pid|pgrep)
        "$BIN" serve -d
        sleep 1
        if [ -f "$PID_FILE" ]; then
            echo "   데몬 재시작됨 (PID: $(cat "$PID_FILE"))"
        else
            echo "   ⚠️  PID 파일이 생성되지 않았습니다. 수동으로 확인하십시오."
        fi
        ;;
    *)
        echo "   이전에 데몬이 실행 중이 아니었습니다. 자동으로 시작하지 않습니다."
        echo "   수동으로 시작하려면: shepherd serve -d"
        echo "   또는 systemd 사용자 유닛: cp deploy/shepherd.service ~/.config/systemd/user/ && systemctl --user enable --now shepherd"
        ;;
esac

echo ""
echo "✅ 설치 완료! 설치된 버전:"
"$BIN" --version 2>/dev/null || true
