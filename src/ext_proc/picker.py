# picker.py
import asyncio
import json
import logging
import os

import aiohttp
import grpc
from envoy.config.core.v3 import base_pb2
from envoy.service.ext_proc.v3 import external_processor_pb2_grpc
from envoy.service.ext_proc.v3.external_processor_pb2 import (
    CommonResponse,
    HeaderMutation,
    HeadersResponse,
    ImmediateResponse,
    ProcessingResponse,
)
from envoy.service.ext_proc.v3.external_processor_pb2_grpc import (
    ExternalProcessorServicer,
    ExternalProcessorStub,
)
from envoy.type.v3 import http_status_pb2
from kubernetes import client, config
from kubernetes.client.rest import ApiException

POOL_LABEL = "app=vllm-mistral"  # Same selector as InferencePool
PORT = 9002
logger = logging.getLogger("picker")

# Initialize Kubernetes client only if not in local mode
LOCAL_MODE = os.getenv("LOCAL_MODE", "false").lower() == "true"
LOCAL_ENDPOINTS = os.getenv("LOCAL_ENDPOINTS", "localhost:8000").split(",")

if not LOCAL_MODE:
    try:
        config.load_incluster_config()  # Load in-cluster config when running in Kubernetes
    except config.ConfigException:
        config.load_kube_config()  # Load local config when running locally
    v1 = client.CoreV1Api()


class RoundRobinPicker:
    def __init__(self):
        self.endpoints = []
        self.current_index = 0
        if LOCAL_MODE:
            self.endpoints = LOCAL_ENDPOINTS
            logger.info(f"Running in local mode with endpoints: {self.endpoints}")

    async def update_endpoints(self):
        """Update the list of available endpoints."""
        if LOCAL_MODE:
            return  # Use the endpoints provided in LOCAL_ENDPOINTS

        try:
            pods = v1.list_pod_for_all_namespaces(label_selector=POOL_LABEL)
            new_endpoints = []
            for pod in pods.items:
                if pod.status.phase == "Running":
                    pod_ip = pod.status.pod_ip
                    if pod_ip:
                        # Get the port from the pod's container spec
                        for container in pod.spec.containers:
                            for port in container.ports:
                                if port.name == "http" or port.container_port == 8000:
                                    new_endpoints.append(
                                        f"{pod_ip}:{port.container_port}"
                                    )
            if new_endpoints:
                self.endpoints = new_endpoints
                logger.info(f"Updated endpoints: {self.endpoints}")
        except ApiException as e:
            logger.error(f"Error getting pod endpoints: {e}")

    def get_next_endpoint(self):
        """Get the next endpoint using round-robin."""
        if not self.endpoints:
            return None

        endpoint = self.endpoints[self.current_index]
        self.current_index = (self.current_index + 1) % len(self.endpoints)
        return endpoint


picker = RoundRobinPicker()


class Picker(ExternalProcessorServicer):
    async def Process(self, request_itr, context):
        async for req in request_itr:
            await picker.update_endpoints()
            target = picker.get_next_endpoint()

            if not target:
                # no healthy backends → send HTTP 503
                resp = ProcessingResponse(
                    immediate_response=ImmediateResponse(
                        status=http_status_pb2.HttpStatus(
                            code=http_status_pb2.ServiceUnavailable
                        ),
                        headers=HeaderMutation(
                            set_headers=[
                                base_pb2.HeaderValueOption(
                                    header=base_pb2.HeaderValue(
                                        key="Content-Type", value="application/json"
                                    )
                                ),
                                base_pb2.HeaderValueOption(
                                    header=base_pb2.HeaderValue(
                                        key="Connection", value="close"
                                    )
                                ),
                            ]
                        ),
                        body=json.dumps({"error": "Service Unavailable"}).encode(
                            "utf-8"
                        ),
                    )
                )
            else:
                if req.HasField("request_headers"):
                    # For header requests, just set the target header
                    resp = ProcessingResponse(
                        request_headers=HeadersResponse(
                            response=CommonResponse(
                                header_mutation=HeaderMutation(
                                    set_headers=[
                                        base_pb2.HeaderValueOption(
                                            header=base_pb2.HeaderValue(
                                                key="x-inference-target", value=target
                                            )
                                        )
                                    ]
                                )
                            )
                        )
                    )
                elif req.HasField("request_body"):
                    try:
                        # Forward the request to the target endpoint
                        async with aiohttp.ClientSession() as session:
                            # Forward the request
                            async with session.post(
                                f"http://{target}/v1/completions",
                                data=req.request_body.body,
                                headers={"Content-Type": "application/json"},
                            ) as response:
                                # Get the response
                                response_body = await response.read()
                                # Create the response with the forwarded content
                                resp = ProcessingResponse(
                                    immediate_response=ImmediateResponse(
                                        status=http_status_pb2.HttpStatus(
                                            code=response.status
                                        ),
                                        headers=HeaderMutation(
                                            set_headers=[
                                                base_pb2.HeaderValueOption(
                                                    header=base_pb2.HeaderValue(
                                                        key="Content-Type",
                                                        value="application/json",
                                                    )
                                                )
                                            ]
                                        ),
                                        body=response_body,
                                    )
                                )
                    except Exception as e:
                        logger.error(f"Error forwarding request to {target}: {e}")
                        resp = ProcessingResponse(
                            immediate_response=ImmediateResponse(
                                status=http_status_pb2.HttpStatus(
                                    code=http_status_pb2.InternalServerError
                                ),
                                headers=HeaderMutation(
                                    set_headers=[
                                        base_pb2.HeaderValueOption(
                                            header=base_pb2.HeaderValue(
                                                key="Content-Type",
                                                value="application/json",
                                            )
                                        )
                                    ]
                                ),
                                body=json.dumps({"error": str(e)}).encode("utf-8"),
                            )
                        )

            yield resp


async def serve():
    server = grpc.aio.server()
    external_processor_pb2_grpc.add_ExternalProcessorServicer_to_server(
        Picker(), server
    )
    server.add_insecure_port(f"[::]:{PORT}")
    await server.start()
    logger.info("picker ready on %s", PORT)
    await server.wait_for_termination()


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    asyncio.run(serve())
