FROM mirror.gcr.io/library/python:3.13-slim

RUN pip install --no-cache-dir uv

WORKDIR /app

COPY pyproject.toml uv.lock ./
RUN uv sync --frozen --no-dev

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
