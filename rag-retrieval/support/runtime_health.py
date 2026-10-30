# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Keep the development container alive and expose a liveness endpoint.

GET /healthz reports container liveness, not remote OpenSearch/LLM access or
NER readiness. It never invokes a model, downloads weights or writes indexes.
"""
from __future__ import annotations

import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class HealthHandler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:
        if self.path != "/healthz":
            self.send_error(404)
            return
        body = json.dumps({"status": "ok", "service": "rag-retrieval-lab"}).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format: str, *args: object) -> None:
        # Health probes should not fill the participant's container logs.
        pass


def main() -> None:
    with ThreadingHTTPServer(("0.0.0.0", 8088), HealthHandler) as server:
        print("Lab health endpoint listening on :8088/healthz", flush=True)
        server.serve_forever()


if __name__ == "__main__":
    main()
