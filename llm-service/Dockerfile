FROM mirror.gcr.io/library/python:3.13-slim

WORKDIR /app

# Hash-pinned export of uv.lock. Regenerate with: make llm-requirements
# Do not install or run uv here. It segfaults under QEMU when cross-building
# linux/amd64 on Apple Silicon.
COPY requirements.txt .
RUN python -m venv /app/.venv \
    && /app/.venv/bin/pip install --no-cache-dir --require-hashes -r requirements.txt

COPY . .

RUN groupadd --gid 65532 llm \
    && useradd --uid 65532 --gid 65532 --no-create-home --shell /bin/false llm

RUN mkdir -p /app/data \
    && chown -R llm:llm /app \
    && chgrp -R 0 /app && chmod -R g=u /app

USER 65532:65532

ENV HOME=/app/data

EXPOSE 50051

CMD ["/app/.venv/bin/python", "-m", "llm.server"]
