package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/wangchen615/hf_downloader/hfdownloader"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	lorav1alpha1 "production-stack.vllm.ai/lora-controller/api/v1alpha1"
	"production-stack.vllm.ai/lora-controller/pkg/placement"
)

// LoraAdapterReconciler reconciles a LoraAdapter object
type LoraAdapterReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Algorithm    placement.Algorithm
	testEndpoint string
}

//+kubebuilder:rbac:groups=production-stack.vllm.ai,resources=loraadapters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=production-stack.vllm.ai,resources=loraadapters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=production-stack.vllm.ai,resources=loraadapters/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

func (r *LoraAdapterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the LoraAdapter instance
	var loraAdapter lorav1alpha1.LoraAdapter
	if err := r.Get(ctx, req.NamespacedName, &loraAdapter); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle different source types
	var adapters []string
	var err error

	adapters, err = r.discoverAdapters(ctx, &loraAdapter)
	if err != nil {
		log.Error(err, "Failed to get adapters")

		// Update status to Pending when adapter discovery fails
		loraAdapter.Status.Phase = "Pending"
		loraAdapter.Status.Message = fmt.Sprintf("Failed to get adapters: %v", err)
		if updateErr := r.Status().Update(ctx, &loraAdapter); updateErr != nil {
			log.Error(updateErr, "Failed to update LoraAdapter status")
			return ctrl.Result{}, updateErr
		}

		// Requeue to retry after some time
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}

	// Use placement algorithm to determine pod assignments
	pods, err := r.Algorithm.PlaceAdapter(ctx, loraAdapter.Spec.BaseModel, loraAdapter.Spec.DeploymentConfig.Algorithm)
	if err != nil {
		log.Error(err, "Failed to determine pod assignments")

		// Update status to Failed when placement fails
		loraAdapter.Status.Phase = "Failed"
		loraAdapter.Status.Message = fmt.Sprintf("Failed to determine pod assignments: %v", err)
		if updateErr := r.Status().Update(ctx, &loraAdapter); updateErr != nil {
			log.Error(updateErr, "Failed to update LoraAdapter status")
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, err
	}

	// Update status with pod assignments and loaded adapters
	now := metav1.Now()
	loadedAdapters := make([]lorav1alpha1.LoadedAdapter, 0, len(adapters))

	// For each adapter path
	for _, adapterPath := range adapters {
		adapterStatus := lorav1alpha1.LoadedAdapter{
			Name:           loraAdapter.Spec.AdapterSource.AdapterName,
			Path:           adapterPath,
			Status:         "Loading",
			LoadTime:       &now,
			PodAssignments: make([]lorav1alpha1.PodAssignment, 0, len(pods)),
		}

		// For each pod, call the load_lora_adapter API
		for _, pod := range pods {
			// Get pod's IP and port
			podIP := pod.Status.PodIP
			if podIP == "" {
				log.Error(fmt.Errorf("pod IP not found"), "Failed to get pod IP", "pod", pod.Name)
				adapterStatus.PodAssignments = append(adapterStatus.PodAssignments, lorav1alpha1.PodAssignment{
					Pod: corev1.ObjectReference{
						Kind:      "Pod",
						Name:      pod.Name,
						Namespace: pod.Namespace,
						UID:       pod.UID,
					},
					Status: "Failed: pod IP not found",
				})
				continue
			}

			assignment, err := r.loadAdapter(ctx, pod, loraAdapter.Spec.AdapterSource.AdapterName, adapterPath)
			adapterStatus.PodAssignments = append(adapterStatus.PodAssignments, *assignment)
			if err != nil {
				log.Error(err, "Failed to load adapter", "pod", pod.Name)
				continue
			}
		}

		// Update adapter status based on pod assignments
		readyCount := 0
		for _, assignment := range adapterStatus.PodAssignments {
			if assignment.Status == "Ready" {
				readyCount++
			}
		}
		if readyCount == len(pods) {
			adapterStatus.Status = "Loaded"
		} else if readyCount > 0 {
			adapterStatus.Status = "PartiallyLoaded"
		} else {
			adapterStatus.Status = "Failed"
		}

		loadedAdapters = append(loadedAdapters, adapterStatus)
	}

	// Update overall status
	allLoaded := true
	for _, adapter := range loadedAdapters {
		if adapter.Status != "Loaded" {
			allLoaded = false
			break
		}
	}

	if allLoaded {
		loraAdapter.Status.Phase = "Ready"
	} else if len(loadedAdapters) > 0 {
		loraAdapter.Status.Phase = "PartiallyReady"
		loraAdapter.Status.Message = "Some adapters failed to load on all pods"
	} else {
		loraAdapter.Status.Phase = "Failed"
		loraAdapter.Status.Message = "Failed to load any adapters"
	}

	loraAdapter.Status.LoadedAdapters = loadedAdapters

	if err := r.Status().Update(ctx, &loraAdapter); err != nil {
		log.Error(err, "Failed to update LoraAdapter status")
		return ctrl.Result{}, err
	}

	// Requeue for s3/cos sources to periodically check for new adapters
	if loraAdapter.Spec.AdapterSource.Type == "s3" || loraAdapter.Spec.AdapterSource.Type == "cos" {
		return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
	}

	return ctrl.Result{}, nil
}

// discoverAdapters finds all adapters matching the pattern in the s3/cos path
func (r *LoraAdapterReconciler) discoverAdapters(ctx context.Context, loraAdapter *lorav1alpha1.LoraAdapter) ([]string, error) {
	log := log.FromContext(ctx)

	// Get the download path from environment variable, default to /models if not set
	downloadBasePath := os.Getenv("ADAPTER_DOWNLOAD_PATH")
	if downloadBasePath == "" {
		downloadBasePath = "/models"
	}

	// Get credentials if specified
	var hfToken string
	if loraAdapter.Spec.AdapterSource.CredentialsSecretRef != nil {
		var secret corev1.Secret
		if err := r.Get(ctx, types.NamespacedName{
			Name:      loraAdapter.Spec.AdapterSource.CredentialsSecretRef.Name,
			Namespace: loraAdapter.Namespace,
		}, &secret); err != nil {
			return nil, fmt.Errorf("failed to get credentials: %w", err)
		}

		// Get credentials based on source type
		switch loraAdapter.Spec.AdapterSource.Type {
		case "huggingface":
			tokenBytes, ok := secret.Data["HUGGING_FACE_HUB_TOKEN"]
			if !ok {
				return nil, fmt.Errorf("secret does not contain HUGGING_FACE_HUB_TOKEN")
			}
			hfToken = string(tokenBytes)
		case "s3", "cos":
			// TODO: S3/COS credentials will be implemented in future
			break
		}
	}

	// Handle different adapter sources
	var adapterPath string
	sourceType := loraAdapter.Spec.AdapterSource.Type
	repository := loraAdapter.Spec.AdapterSource.Repository
	adapterName := loraAdapter.Spec.AdapterSource.AdapterName

	switch sourceType {
	case "huggingface":
		// Construct local path for the adapter using the configured download path
		localPath := fmt.Sprintf("%s/%s", downloadBasePath, adapterName)

		// Check if adapter files already exist
		if loraAdapter.Spec.AdapterSource.AdapterPath != "" {
			// Check if the path contains safetensors files
			if _, err := findAdapterDir(loraAdapter.Spec.AdapterSource.AdapterPath); err == nil {
				log.Info("Adapter files already exist, skipping download",
					"path", loraAdapter.Spec.AdapterSource.AdapterPath)
				return []string{loraAdapter.Spec.AdapterSource.AdapterPath}, nil
			}
		}

		log.Info("Downloading adapter from Huggingface",
			"repository", repository,
			"adapter", adapterName,
			"localPath", localPath)

		// Create downloader and set token via environment variable
		downloader := hfdownloader.NewDownloader()
		if hfToken != "" {
			os.Setenv("HF_TOKEN", hfToken)
		}
		downloader.SetCustomPath(localPath)

		// Download the adapter
		downloadPath, err := downloader.Download(repository, "main")
		if err != nil {
			return nil, fmt.Errorf("failed to download adapter from Huggingface: %w", err)
		}

		// Find the directory containing the adapter files
		adapterDir, err := findAdapterDir(downloadPath)
		if err != nil {
			return nil, fmt.Errorf("failed to find adapter directory: %w", err)
		}

		// Update AdapterPath with the actual download location
		loraAdapter.Spec.AdapterSource.AdapterPath = adapterDir
		if err := r.Update(ctx, loraAdapter); err != nil {
			log.Error(err, "Failed to update adapter path in spec")
			// Don't fail the operation, just log the error
		}

		adapterPath = adapterDir

	case "s3", "cos":
		// TODO: Implement S3/COS adapter download in future
		log.Info("S3/COS adapter download not implemented yet",
			"repository", repository,
			"adapter", adapterName)

		localPath := fmt.Sprintf("%s/%s", downloadBasePath, adapterName)

		// Update AdapterPath with the expected download location
		loraAdapter.Spec.AdapterSource.AdapterPath = localPath
		if err := r.Update(ctx, loraAdapter); err != nil {
			log.Error(err, "Failed to update adapter path in spec")
			// Don't fail the operation, just log the error
		}

		// TODO: This is a placeholder. In the actual implementation:
		// 1. List objects in the S3/COS bucket
		// 2. Filter them using the pattern if specified
		// 3. Download the filtered objects
		adapters := []string{localPath}

		// Filter objects based on pattern if specified
		if pattern := loraAdapter.Spec.AdapterSource.Pattern; pattern != "" {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("invalid pattern %q: %w", pattern, err)
			}
			filtered := make([]string, 0)
			for _, obj := range adapters {
				if re.MatchString(obj) {
					filtered = append(filtered, obj)
				}
			}
			adapters = filtered
		}

		// Limit number of adapters if specified
		if max := loraAdapter.Spec.AdapterSource.MaxAdapters; max != nil && int32(len(adapters)) > *max {
			adapters = adapters[:*max]
		}

		return adapters, fmt.Errorf("S3/COS adapter download not implemented yet")

	case "local":
		// For local adapters, verify the path is provided
		if loraAdapter.Spec.AdapterSource.AdapterPath == "" {
			return nil, fmt.Errorf("adapterPath is required for local adapter source")
		}
		adapterPath = loraAdapter.Spec.AdapterSource.AdapterPath

	default:
		return nil, fmt.Errorf("unsupported adapter source type: %s", sourceType)
	}

	adapters := []string{adapterPath}

	// Limit number of adapters if specified
	if max := loraAdapter.Spec.AdapterSource.MaxAdapters; max != nil && int32(len(adapters)) > *max {
		adapters = adapters[:*max]
	}

	return adapters, nil
}

// findAdapterDir looks for a directory containing safetensors files
func findAdapterDir(basePath string) (string, error) {
	// Use os.ReadDir to walk through directories
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return "", fmt.Errorf("failed to read directory %s: %w", basePath, err)
	}

	// First check if safetensors files are in current directory
	for _, entry := range entries {
		if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".safetensors") || strings.HasSuffix(entry.Name(), ".bin")) {
			return basePath, nil
		}
	}

	// Then check subdirectories
	for _, entry := range entries {
		if entry.IsDir() {
			subPath := filepath.Join(basePath, entry.Name())
			if entry.Name() == "snapshots" {
				// Check snapshot subdirectories
				snapshots, err := os.ReadDir(subPath)
				if err != nil {
					continue
				}
				for _, snapshot := range snapshots {
					if snapshot.IsDir() {
						snapshotPath := filepath.Join(subPath, snapshot.Name())
						files, err := os.ReadDir(snapshotPath)
						if err != nil {
							continue
						}
						for _, file := range files {
							if !file.IsDir() && (strings.HasSuffix(file.Name(), ".safetensors") || strings.HasSuffix(file.Name(), ".bin")) {
								return snapshotPath, nil
							}
						}
					}
				}
			}
		}
	}

	return "", fmt.Errorf("no directory containing safetensors or bin files found in %s", basePath)
}

// loadAdapter attempts to load a LoRA adapter into a pod
func (r *LoraAdapterReconciler) loadAdapter(ctx context.Context, pod corev1.Pod, adapterName string, adapterPath string) (*lorav1alpha1.PodAssignment, error) {
	assignment := &lorav1alpha1.PodAssignment{
		Pod: corev1.ObjectReference{
			Kind:      "Pod",
			Name:      pod.Name,
			Namespace: pod.Namespace,
			UID:       pod.UID,
		},
		Status: "Loading",
	}

	// Build the endpoint URL
	endpoint := fmt.Sprintf("http://%s:8000/v1/load_lora_adapter", pod.Status.PodIP)
	if r.testEndpoint != "" {
		endpoint = r.testEndpoint
	}

	// First check if adapter is already loaded
	checkEndpoint := fmt.Sprintf("http://%s:8000/v1/models", pod.Status.PodIP)
	if r.testEndpoint != "" {
		// For testing, use the same endpoint but different path
		checkEndpoint = strings.TrimSuffix(r.testEndpoint, "/load_lora_adapter") + "/models"
	}

	// Make the HTTP request to check models
	req, err := http.NewRequestWithContext(ctx, "GET", checkEndpoint, nil)
	if err != nil {
		assignment.Status = fmt.Sprintf("Failed: %v", err)
		return assignment, err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		assignment.Status = fmt.Sprintf("Failed: %v", err)
		return assignment, err
	}
	defer resp.Body.Close()

	// Parse response to check if adapter is already loaded
	var modelsResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		assignment.Status = fmt.Sprintf("Failed: %v", err)
		return assignment, err
	}

	// Check if adapter is already loaded
	for _, model := range modelsResp.Data {
		if strings.Contains(model.ID, adapterName) {
			assignment.Status = "Ready"
			return assignment, nil
		}
	}

	// If not loaded, proceed with loading
	payload := map[string]string{
		"lora_name": adapterName,
		"lora_path": adapterPath,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		assignment.Status = fmt.Sprintf("Failed: %v", err)
		return assignment, err
	}

	// Make the HTTP request to load adapter
	req, err = http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(payloadBytes))
	if err != nil {
		assignment.Status = fmt.Sprintf("Failed: %v", err)
		return assignment, err
	}

	req.Header.Set("Content-Type", "application/json")
	client = &http.Client{}
	resp, err = client.Do(req)
	if err != nil {
		assignment.Status = fmt.Sprintf("Failed: %v", err)
		return assignment, err
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		assignment.Status = fmt.Sprintf("Failed: %v", err)
		return assignment, err
	}

	// Check response status
	if resp.StatusCode == http.StatusBadRequest {
		// Parse error response
		var errorResp struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(body, &errorResp); err == nil && strings.Contains(errorResp.Message, "already been loaded") {
			// If error indicates adapter is already loaded, consider it a success
			assignment.Status = "Ready"
			return assignment, nil
		}
		assignment.Status = fmt.Sprintf("Failed: status code %d - %s", resp.StatusCode, string(body))
		return assignment, fmt.Errorf(assignment.Status)
	}

	if resp.StatusCode != http.StatusOK {
		assignment.Status = fmt.Sprintf("Failed: status code %d - %s", resp.StatusCode, string(body))
		return assignment, fmt.Errorf(assignment.Status)
	}

	assignment.Status = "Ready"
	return assignment, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *LoraAdapterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&lorav1alpha1.LoraAdapter{}).
		Complete(r)
}

// Note: Implementation of initS3Client, initCOSClient, and listStorageObjects would go here
// These would handle the actual S3/COS interactions
