.PHONY: serve

serve:
	@python3 -m http.server 8000 --directory . & \
	server_pid=$$!; \
	trap 'kill "$$server_pid" 2>/dev/null || true; wait "$$server_pid" 2>/dev/null || true' EXIT INT TERM; \
	python3 -m webbrowser -t http://127.0.0.1:8000/www/; \
	wait "$$server_pid"