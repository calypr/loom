package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/repositoryseed"
)

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: loom-repository <preflight|launch> [options]")
	}
	switch os.Args[1] {
	case "preflight":
		manifest, _, _ := flagsAndPrepare("preflight", os.Args[2:])
		if err := json.NewEncoder(os.Stdout).Encode(manifest); err != nil {
			fatalf("encode manifest: %v", err)
		}
	case "launch":
		manifest, loomRoot, options := flagsAndPrepare("launch", os.Args[2:])
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := launch(ctx, loomRoot, manifest, options); err != nil {
			fatalf("repository launch failed: %v", err)
		}
	default:
		fatalf("unknown command %q", os.Args[1])
	}
}

type launchOptions struct {
	composeProject string
	apiHost        string
	apiPort        int
	uiHost         string
	uiPort         int
}

var composeProjectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func flagsAndPrepare(name string, args []string) (repositoryseed.Manifest, string, launchOptions) {
	composeProjectDefault := ""
	apiHostDefault, apiPortDefault := "127.0.0.1", 8080
	uiHostDefault, uiPortDefault := "127.0.0.1", 3080
	if name == "launch" {
		composeProjectDefault = os.Getenv("LOOM_REPOSITORY_COMPOSE_PROJECT")
		apiHostDefault = envOrDefault("LOOM_REPOSITORY_API_HOST", apiHostDefault)
		apiPortDefault = envPort("LOOM_REPOSITORY_API_PORT", apiPortDefault)
		uiHostDefault = envOrDefault("LOOM_REPOSITORY_UI_HOST", uiHostDefault)
		uiPortDefault = envPort("LOOM_REPOSITORY_UI_PORT", uiPortDefault)
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	repository := fs.String("repository", ".", "repository containing META and CONFIG")
	config := fs.String("config", "", "CONFIG file, relative to the repository")
	project := fs.String("project", "", "canonical PROGRAM/PROJECT override")
	loomRoot := fs.String("loom-root", ".", "Loom source checkout")
	composeProject := fs.String("compose-project", composeProjectDefault, "Docker Compose project name")
	apiHost := fs.String("api-host", apiHostDefault, "host address for the Loom API")
	apiPort := fs.Int("api-port", apiPortDefault, "host port for the Loom API")
	uiHost := fs.String("ui-host", uiHostDefault, "host address for the Loom UI")
	uiPort := fs.Int("ui-port", uiPortDefault, "host port for the Loom UI")
	_ = fs.Parse(args)
	manifest, err := repositoryseed.Prepare(repositoryseed.Options{Repository: *repository, Config: *config, Project: *project})
	if err != nil {
		fatalf("repository preflight failed: %v", err)
	}
	root, err := filepath.Abs(*loomRoot)
	if err != nil {
		fatalf("resolve Loom source: %v", err)
	}
	if name != "launch" {
		return manifest, root, launchOptions{}
	}
	if *composeProject == "" {
		*composeProject = repositoryComposeProject(manifest.Repository)
	}
	options, err := newLaunchOptions(*composeProject, *apiHost, *apiPort, *uiHost, *uiPort)
	if err != nil {
		fatalf("invalid launch configuration: %v", err)
	}
	return manifest, root, options
}

func newLaunchOptions(composeProject, apiHost string, apiPort int, uiHost string, uiPort int) (launchOptions, error) {
	if !composeProjectPattern.MatchString(composeProject) {
		return launchOptions{}, fmt.Errorf("Compose project %q must match %s", composeProject, composeProjectPattern)
	}
	if strings.TrimSpace(apiHost) == "" || strings.TrimSpace(uiHost) == "" {
		return launchOptions{}, errors.New("API and UI hosts must not be empty")
	}
	if apiPort < 1 || apiPort > 65535 || uiPort < 1 || uiPort > 65535 {
		return launchOptions{}, errors.New("API and UI ports must be between 1 and 65535")
	}
	return launchOptions{composeProject: composeProject, apiHost: apiHost, apiPort: apiPort, uiHost: uiHost, uiPort: uiPort}, nil
}

func (options launchOptions) composeArgs(loomRoot string) []string {
	return []string{"compose", "--project-name", options.composeProject, "-f", filepath.Join(loomRoot, "compose.yaml"), "-f", filepath.Join(loomRoot, "compose.repository.yaml")}
}

func (options launchOptions) composeEnvironment() []string {
	return []string{
		"LOOM_COMPOSE_PROJECT_NAME=" + options.composeProject,
		"LOOM_API_HOST=" + options.apiHost,
		"LOOM_API_PORT=" + strconv.Itoa(options.apiPort),
		"LOOM_UI_HOST=" + options.uiHost,
		"LOOM_UI_PORT=" + strconv.Itoa(options.uiPort),
	}
}

func (options launchOptions) apiURL() string {
	return "http://" + net.JoinHostPort(connectHost(options.apiHost), strconv.Itoa(options.apiPort))
}

func (options launchOptions) uiURL() string {
	return "http://" + net.JoinHostPort(connectHost(options.uiHost), strconv.Itoa(options.uiPort))
}

func connectHost(host string) string {
	switch host {
	case "0.0.0.0":
		return "127.0.0.1"
	case "::":
		return "::1"
	default:
		return host
	}
}

func repositoryComposeProject(repository string) string {
	sum := sha256.Sum256([]byte(repository))
	return fmt.Sprintf("loom-repository-%x", sum[:6])
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envPort(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	port, err := strconv.Atoi(value)
	if err != nil {
		fatalf("invalid %s: %v", name, err)
	}
	return port
}

func launch(ctx context.Context, loomRoot string, manifest repositoryseed.Manifest, options launchOptions) error {
	compose := options.composeArgs(loomRoot)
	environment := append(os.Environ(),
		"LOOM_REPOSITORY_CONFIG_DIR="+manifest.ConfigDir,
		"LOOM_REPOSITORY_CONFIG_NAME="+manifest.ConfigName,
		"LOOM_REPOSITORY_PROJECT="+manifest.Project,
	)
	environment = append(environment, options.composeEnvironment()...)
	runDocker := func(arguments ...string) error {
		command := exec.CommandContext(ctx, "docker", append(compose, arguments...)...)
		command.Dir, command.Env, command.Stdout, command.Stderr = loomRoot, environment, os.Stdout, os.Stderr
		return command.Run()
	}
	fmt.Printf("[1/6] repository validated project=%s generation=%s files=%d\n", manifest.Project, manifest.Generation, len(manifest.Files))
	if err := stopStaleUI(runDocker); err != nil {
		return fmt.Errorf("stop stale UI: %w", err)
	}
	if err := runDocker("up", "--build", "-d", "arangodb", "clickhouse", "loom-api"); err != nil {
		return fmt.Errorf("start infrastructure: %w", err)
	}
	fmt.Println("[2/6] waiting for Loom API")
	client := &http.Client{Timeout: 10 * time.Minute}
	apiURL := options.apiURL()
	uiURL := options.uiURL()
	if err := waitHTTP(ctx, client, apiURL+"/readyz", 3*time.Minute); err != nil {
		return err
	}
	fmt.Println("[3/6] checking repository META generation")
	load, err := loadRepositoryMeta(ctx, client, apiURL, manifest)
	if err != nil {
		return err
	}
	fmt.Printf("      generation response reused=%v\n", load["reused"])
	fmt.Println("[4/6] compiling and publishing CONFIG into ClickHouse")
	publication, err := publish(ctx, client, apiURL, manifest)
	if err != nil {
		return err
	}
	if activated, _ := publication["activated"].(bool); !activated {
		return fmt.Errorf("publication did not activate the repository generation")
	}
	executionID, _ := publication["executionId"].(string)
	if executionID == "" {
		return errors.New("publication response omitted executionId")
	}
	fmt.Println("[5/6] verifying publication and Explorer state")
	if _, err := getJSON(ctx, client, apiURL+"/api/v1/dataframe/recipe-executions/"+url.PathEscape(executionID)); err != nil {
		return fmt.Errorf("verify ClickHouse execution: %w", err)
	}
	viewerPath := viewerURL(apiURL, manifest)
	if _, err := getJSON(ctx, client, viewerPath); err != nil {
		return fmt.Errorf("verify Explorer: %w", err)
	}
	fmt.Println("[6/6] starting local UI")
	if err := runDocker("up", "--build", "--no-deps", "-d", "loom-ui"); err != nil {
		return fmt.Errorf("start UI: %w", err)
	}
	if err := waitHTTP(ctx, client, uiURL+"/", 2*time.Minute); err != nil {
		return err
	}
	query := url.Values{"project": {manifest.Project}, "explorer": {"default"}}
	baseUI, err := url.Parse(uiURL)
	if err != nil {
		return err
	}
	builder := *baseUI
	builder.Path, builder.RawQuery = "/", query.Encode()+"&mode=builder"
	viewer := *baseUI
	viewer.Path, viewer.RawQuery = "/", query.Encode()+"&mode=viewer"
	fmt.Printf("Loom repository demo is ready\n  project: %s\n  generation: %s\n  Builder: %s\n  Viewer:  %s\n  Publish writes atomically to: %s\n", manifest.Project, manifest.Generation, builder.String(), viewer.String(), manifest.ConfigPath)
	return nil
}

func stopStaleUI(runDocker func(...string) error) error {
	return runDocker("stop", "loom-ui")
}

func loadRepositoryMeta(ctx context.Context, client *http.Client, base string, manifest repositoryseed.Manifest) (map[string]any, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, generationStatusURL(base, manifest), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("check generation status: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	switch response.StatusCode {
	case http.StatusNotFound:
		fmt.Println("      generation is absent; loading repository META")
		return upload(ctx, client, base, manifest)
	case http.StatusOK:
		var value map[string]any
		if err := json.Unmarshal(body, &value); err != nil {
			return nil, fmt.Errorf("decode generation status: %w", err)
		}
		reusable, ok := value["reusable"].(bool)
		if !ok {
			return nil, errors.New("generation status response omitted reusable")
		}
		if reusable {
			fmt.Println("      generation is already reusable; skipping META upload")
			value["reused"] = true
			return value, nil
		}
		state, _ := value["state"].(string)
		if state == "" {
			state = "unknown"
		}
		return nil, fmt.Errorf("generation %s/%s already exists in state %s and is not reusable", manifest.Project, manifest.Generation, state)
	default:
		return nil, fmt.Errorf("%s returned %s: %s", request.URL.Path, response.Status, strings.TrimSpace(string(body)))
	}
}

func upload(ctx context.Context, client *http.Client, base string, manifest repositoryseed.Manifest) (map[string]any, error) {
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	done := make(chan error, 1)
	go func() {
		var result error
		for _, input := range manifest.Files {
			path := filepath.Join(manifest.Repository, filepath.FromSlash(input.Path))
			file, err := os.Open(path)
			if err != nil {
				result = err
				break
			}
			part, err := multipartWriter.CreateFormFile("file", filepath.Base(path))
			if err == nil {
				_, err = io.Copy(part, file)
			}
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				result = err
				break
			}
		}
		if result == nil {
			_ = multipartWriter.WriteField("project", manifest.Project)
			_ = multipartWriter.WriteField("generation", manifest.Generation)
			_ = multipartWriter.WriteField("defer_activation", "false")
			result = multipartWriter.Close()
		}
		_ = writer.CloseWithError(result)
		done <- result
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL(base, manifest), reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	value, err := doJSON(client, request)
	writeErr := <-done
	if err != nil {
		return nil, fmt.Errorf("load generation: %w", err)
	}
	if writeErr != nil {
		return nil, fmt.Errorf("stream META: %w", writeErr)
	}
	return value, nil
}

func publish(ctx context.Context, client *http.Client, base string, manifest repositoryseed.Manifest) (map[string]any, error) {
	path := publishURL(base, manifest)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(manifest.Workspace))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Loom-Source-Commit", manifest.SourceCommit)
	value, err := doJSON(client, request)
	if err != nil {
		return nil, fmt.Errorf("publish CONFIG: %w", err)
	}
	return value, nil
}

// Generation storage uses legacy project IDs while Explorer APIs expose canonical IDs.
func uploadURL(base string, manifest repositoryseed.Manifest) string {
	return strings.TrimRight(base, "/") + "/api/v1/datasets/" + url.PathEscape(projectid.Legacy(manifest.Project)) + "/generations/" + url.PathEscape(manifest.Generation)
}

func generationStatusURL(base string, manifest repositoryseed.Manifest) string {
	return uploadURL(base, manifest)
}

func publishURL(base string, manifest repositoryseed.Manifest) string {
	return strings.TrimRight(base, "/") + "/api/v1/projects/" + url.PathEscape(projectid.Canonical(manifest.Project)) + "/generations/" + url.PathEscape(manifest.Generation) + "/explorer-config"
}

func viewerURL(base string, manifest repositoryseed.Manifest) string {
	return strings.TrimRight(base, "/") + "/api/v1/projects/" + url.PathEscape(projectid.Canonical(manifest.Project)) + "/explorers/default"
}

func getJSON(ctx context.Context, client *http.Client, path string) (map[string]any, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	return doJSON(client, request)
}

func doJSON(client *http.Client, request *http.Request) (map[string]any, error) {
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s returned %s: %s", request.URL.Path, response.Status, strings.TrimSpace(string(body)))
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, fmt.Errorf("decode %s: %w", request.URL.Path, err)
	}
	return value, nil
}

func waitHTTP(ctx context.Context, client *http.Client, address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("timed out waiting for %s", address)
}

func fatalf(format string, arguments ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}
