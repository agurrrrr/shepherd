.PHONY: build web clean install build-go dev serve

# Svelte 프론트엔드 빌드
web:
	cd web && npm install && npm run build
	rm -rf internal/server/web_dist
	cp -r web/build internal/server/web_dist
	touch internal/server/web_dist/.gitkeep

# Go 바이너리 빌드 (Svelte 포함)
build: web
	go build -ldflags "-X main.buildTime=$$(date -Iseconds)" -o shepherd ./cmd/shepherd

# 빌드 + 설치
install: build
	rm -f ~/.local/bin/shepherd
	cp shepherd ~/.local/bin/

# Go만 빌드 (프론트엔드 변경 없을 때)
build-go:
	go build -ldflags "-X main.buildTime=$$(date -Iseconds)" -o shepherd ./cmd/shepherd

# Go 빌드 + 설치 (프론트엔드 변경 없을 때)
install-go: build-go
	rm -f ~/.local/bin/shepherd
	cp shepherd ~/.local/bin/

# 개발 모드 안내
dev:
	@echo "터미널 1: shepherd serve"
	@echo "터미널 2: cd web && npm run dev"

# 정리
clean:
	rm -f shepherd
	# .gitkeep은 남긴다: //go:embed all:web_dist는 이 디렉터리에 파일이 하나도
	# 없으면 컴파일 자체가 실패한다 (#840). 빌드 산출물만 지운다.
	find internal/server/web_dist -mindepth 1 ! -name .gitkeep -delete
	rm -rf web/build
	rm -rf web/node_modules
