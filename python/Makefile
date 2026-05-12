.PHONY: sync lock check lint format type test all clean

sync:
	uv sync

lock:
	uv lock

lint:
	uv run ruff check .

format:
	uv run ruff format .

type:
	uv run mypy

test:
	uv run pytest

check: lint type test

all: sync check

clean:
	find . -type d -name __pycache__ -prune -exec rm -rf {} +
	find . -type d -name .pytest_cache -prune -exec rm -rf {} +
	find . -type d -name .mypy_cache -prune -exec rm -rf {} +
	find . -type d -name .ruff_cache -prune -exec rm -rf {} +
	find . -type d -name "*.egg-info" -prune -exec rm -rf {} +
	rm -rf .venv
