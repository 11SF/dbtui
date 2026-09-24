BIN := gui/build/bin/dbtui-gui

UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
# Works around a linker error ("_OBJC_CLASS_$_UTType" undefined) that shows
# up building wails' desktop/darwin package against recent macOS SDKs.
export CGO_LDFLAGS := -framework UniformTypeIdentifiers
endif

.PHONY: run build frontend clean

# Build the embedded frontend and the app binary, then launch it.
run: build
	@pkill -f "$(BIN)" 2>/dev/null || true
	./$(BIN)

build: frontend
	mkdir -p gui/build/bin
	cd gui && go build -tags desktop,production -o build/bin/dbtui-gui .

frontend:
	cd gui/frontend && npm install && npm run build

clean:
	rm -rf gui/build/bin gui/frontend/dist
