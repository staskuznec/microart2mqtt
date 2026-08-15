BINARY = microart2mqtt

# Версия попадает в бинарник: её отдаёт флаг -version и страница «Обзор».
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -s -w -X main.version=$(VERSION)

# CGO не нужен: SQLite взят чистый на Go, поэтому сборка под ARMv7 ничем не
# отличается от сборки под хост.
ARM = GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0

# Куда ставить при make deploy. Переопределяется: make deploy HOST=1.2.3.4
USER ?= root
HOST ?= 192.168.20.218

.PHONY: build build-armv7 agent agent-armv7 image run test fmt clean deploy logs

build:
	go build -ldflags "$(LDFLAGS)" -o ./exe/$(BINARY) ./cmd/$(BINARY)

build-armv7:
	$(ARM) go build -ldflags "$(LDFLAGS)" -o ./exe/$(BINARY)-armv7 ./cmd/$(BINARY)

# Агент для «Малины» (крутится на устройстве)
agent:
	go build -ldflags "$(LDFLAGS)" -o ./exe/malina-agent ./cmd/malina-agent

agent-armv7:
	$(ARM) go build -ldflags "$(LDFLAGS)" -o ./exe/microart-boot/malina-agent ./cmd/malina-agent

# Готовый к прошивке образ: вписывает автозапуск и кладёт бандл агента прямо в
# /boot образа. После dd на карту копировать руками уже ничего не нужно.
# Свой образ указывается так: make image IMAGE=docs/другой.img
IMAGE ?= docs/malina2_5.08_u43_everything_shrunk.img
image: agent-armv7
	python3 tools/malina_bootstrap.py $(IMAGE) --apply
	python3 tools/fat_put.py $(IMAGE) MICROART \
		./exe/microart-boot/malina-agent=AGENT \
		deploy/malina/agent-ctl.sh=AGENTCTL.SH \
		deploy/malina/install-on-malina.sh=INSTALL.SH \
		deploy/malina/microart-mqtt.service=AGENT.SRV
	@echo "Образ готов: $(IMAGE) — можно писать на карту (dd)."

# Локальный запуск: база кладётся в ./exe, веб на 8081.
run: build
	./exe/$(BINARY) --db ./exe/$(BINARY).db --addr 127.0.0.1:8081 --log-level debug

test: fmt
	go vet ./...
	go test -race ./...
	sh -n install.sh

# gofmt -l сам по себе завершается успешно, даже когда что-то нашёл, поэтому
# проверяем, что вывод пустой: иначе неотформатированный код молча проезжает.
fmt:
	@test -z "$$(gofmt -l .)" || \
		{ echo "не отформатировано:"; gofmt -l .; exit 1; }

# Быстрая выкатка своей сборки, минуя релиз: полезно при отладке на объекте.
# Обычная установка — через install.sh, см. README.
deploy: build-armv7
	scp ./exe/$(BINARY)-armv7 $(USER)@$(HOST):/tmp/$(BINARY)
	ssh $(USER)@$(HOST) 'systemctl stop $(BINARY) 2>/dev/null || true; \
		install -m 0755 /tmp/$(BINARY) /opt/$(BINARY)/$(BINARY); \
		rm -f /tmp/$(BINARY); \
		systemctl start $(BINARY); \
		systemctl --no-pager status $(BINARY) | head -5'

logs:
	ssh $(USER)@$(HOST) "journalctl -u $(BINARY) -f -n 50"

clean:
	rm -f ./exe/$(BINARY) ./exe/$(BINARY)-armv7 ./exe/$(BINARY).db
