import asyncio
import json

import grpc
from envoy.config.core.v3 import base_pb2
from envoy.service.ext_proc.v3.external_processor_pb2 import (
    CommonResponse,
    HeaderMutation,
    HeadersResponse,
    HttpBody,
    HttpHeaders,
    ProcessingRequest,
)
from envoy.service.ext_proc.v3.external_processor_pb2_grpc import ExternalProcessorStub


async def run():
    # Create a gRPC channel to the picker
    async with grpc.aio.insecure_channel("localhost:8080") as channel:
        # Create a stub (client)
        stub = ExternalProcessorStub(channel)

        # Create a vLLM-compatible request body
        vllm_request = {
            "model": "mistralai/Mistral-7B-Instruct-v0.2",
            "prompt": "Once upon a time,",
            "max_tokens": 10,
        }

        # Create a single request combining headers and body
        request_body = json.dumps(vllm_request).encode("utf-8")
        headers = [
            ("content-type", "application/json"),
            ("content-length", str(len(request_body))),
            ("user-agent", "curl/7.81.0"),
            ("host", "localhost:8080"),
            ("accept", "*/*"),
            (":path", "/v1/completions"),
            (":method", "POST"),
        ]

        # Create the combined request
        request = ProcessingRequest(
            request_headers=HttpHeaders(
                headers=base_pb2.HeaderMap(
                    headers=[base_pb2.HeaderValue(key=k, value=v) for k, v in headers]
                )
            ),
            request_body=HttpBody(body=request_body, end_of_stream=True),
        )

        # Send the combined request and handle response
        async for response in stub.Process([request]):
            if response.HasField("request_headers"):
                # Print the target endpoint that was assigned
                header_mutation = response.request_headers.response.header_mutation
                if header_mutation and header_mutation.set_headers:
                    for header in header_mutation.set_headers:
                        if header.header.key == "x-inference-target":
                            print(f"Request will be routed to: {header.header.value}")
            elif response.HasField("immediate_response"):
                # Handle the actual response from the vLLM server
                status_code = response.immediate_response.status.code
                body = response.immediate_response.body.decode("utf-8")
                print(f"Response status: {status_code}")
                print(f"Response body: {body}")


if __name__ == "__main__":
    asyncio.run(run())
