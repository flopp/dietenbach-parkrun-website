.PHONY: generate serve

generate:
	@go run ./generator/main.go -output .out -config secrets.json

serve:
	@python3 -m http.server 8000 --directory .out & \
	server_pid=$$!; \
	trap 'kill "$$server_pid" 2>/dev/null || true; wait "$$server_pid" 2>/dev/null || true' EXIT INT TERM; \
	python3 -m webbrowser -t http://127.0.0.1:8000/; \
	wait "$$server_pid"


upload: generate
	@rsync \
		-avz \
		.out/ \
		echeclus.uberspace.de:/var/www/virtual/floppnet/parkrun.freiburg.run/