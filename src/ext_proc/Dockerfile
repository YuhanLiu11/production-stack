# ----------- picker image -------------
    FROM python:3.11-slim

    # 1. System deps (none except ca‑certs in slim, so we’re good)

    WORKDIR /app
    COPY picker.py .

    # 2. Python deps
    #    - grpcio: gRPC runtime
    #    - protobuf: runtime for *_pb2 modules
    #    - xds‑protos: all Envoy/XDS generated stubs (incl. ext_proc)
    RUN pip install --no-cache-dir \
            grpcio==1.62.1 \
            protobuf==5.26.1 \
            kubernetes==32.0.0 \
            xds-protos==1.71.0 \
            aiohttp

    EXPOSE 9002
    CMD ["python", "picker.py"]
