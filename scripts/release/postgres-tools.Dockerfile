FROM postgres:18.6-alpine
COPY scripts/ops /ops
COPY scripts/release/restore-smoke-runner.sh /restore-smoke-runner.sh
ENTRYPOINT ["sh", "/restore-smoke-runner.sh"]
