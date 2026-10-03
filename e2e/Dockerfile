FROM golang:1.27.1 AS builder
# go.mod can ask for a newer Go patch release than the image has
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG BUILD_TAG=v0.1.0
RUN CGO_ENABLED=0 go build -ldflags "-X github.com/hpcsc/mutants/internal/version.releaseTag=${BUILD_TAG}" \
    -o /mutants ./cmd/mutants

FROM node:24-trixie-slim
# python3, make and g++ build node-pty when no prebuilt node-pty fits the platform
RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 python3-venv make g++ git ca-certificates \
    && rm -rf /var/lib/apt/lists/*
# the venvs of the Python fixtures: globalSetup.ts makes them when these variables are not set
ARG PYTEST_VERSION=9.1.1
ARG COVERAGE_VERSION=7.16.2
RUN python3 -m venv /opt/venvs/with-coverage \
    && /opt/venvs/with-coverage/bin/pip install --quiet "pytest==${PYTEST_VERSION}" "coverage==${COVERAGE_VERSION}" \
    && python3 -m venv /opt/venvs/without-coverage \
    && /opt/venvs/without-coverage/bin/pip install --quiet "pytest==${PYTEST_VERSION}"
ENV E2E_PYTHON_VENV=/opt/venvs/with-coverage E2E_PYTHON_VENV_WITHOUT_COVERAGE=/opt/venvs/without-coverage
COPY --from=builder /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=local
# the platform package of ast-grep holds the binary, so it needs no install script
ARG AST_GREP_VERSION=0.45.1
RUN arch="$(dpkg --print-architecture | sed 's/amd64/x64/')" \
    && npm install --global "@ast-grep/cli-linux-${arch}-gnu@${AST_GREP_VERSION}" \
    && ln -s "$(npm root --global)/@ast-grep/cli-linux-${arch}-gnu/ast-grep" /usr/local/bin/ast-grep \
    && ast-grep --version
WORKDIR /e2e
COPY e2e/package.json e2e/package-lock.json* ./
RUN if [ -f package-lock.json ]; then npm ci; else npm install --no-audit --no-fund; fi
COPY e2e ./
COPY --from=builder /mutants /usr/local/bin/mutants
ARG BUILD_TAG=v0.1.0
ENV EXECUTABLE=/usr/local/bin/mutants BUILD_TAG=${BUILD_TAG}
CMD ["npx", "vitest", "run"]
