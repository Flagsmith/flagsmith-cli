package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

var (
	apiMethodFlag   string
	apiFieldFlags   []string
	apiRawFields    []string
	apiInputFlag   string
	apiHeaderFlags []string
	apiIncludeFlag bool
	apiSDKFlag     bool
)

var apiCmd = &cobra.Command{
	Use:   "api <path>",
	Short: "Call the Flagsmith API with the CLI's credentials",
	Args:  cobra.ExactArgs(1),
	RunE:  runAPI,
}

func runAPI(cmd *cobra.Command, args []string) error {
	pc, err := applyContext(cmd)
	if err != nil {
		return err
	}

	baseURL, applyAuth, err := apiTarget(cmd, pc)
	if err != nil {
		return err
	}

	headers, err := parseHeaders(apiHeaderFlags)
	if err != nil {
		return err
	}

	body, contentType, query, method, err := apiRequestBody(cmd)
	if err != nil {
		return err
	}

	full := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(args[0], "/")
	if len(query) > 0 {
		full += "?" + query.Encode()
	}

	resp, respBody, err := apiDo(cmd.Context(), method, full, body, contentType, headers, applyAuth)
	if err != nil {
		return err
	}
	if !statusOK(resp.StatusCode) {
		return apiError(cmd, method, full, resp, respBody)
	}

	out := cmd.OutOrStdout()
	if apiIncludeFlag {
		writeStatusLine(out, resp)
	}
	return writeBody(out, respBody, jqFlag)
}

// apiTarget resolves the base URL and the auth applier for the chosen surface.
func apiTarget(cmd *cobra.Command, pc *projectContext) (string, func(*http.Request), error) {
	if apiSDKFlag {
		key, err := sdkEnvironmentKey(pc)
		if err != nil {
			return "", nil, err
		}
		return pc.SDKAPIURL.Value.(string), func(r *http.Request) {
			r.Header.Set("X-Environment-Key", key)
		}, nil
	}
	cred, err := resolveCredential(cmd.Context())
	if err != nil {
		return "", nil, err
	}
	return apiURL, cred.auth.Apply, nil
}

// sdkEnvironmentKey returns the client- or server-side key for --sdk calls,
// taken verbatim from the environment context (no Admin name resolution).
func sdkEnvironmentKey(pc *projectContext) (string, error) {
	if v := os.Getenv("FLAGSMITH_ENVIRONMENT_KEY"); v != "" {
		return v, nil
	}
	if k, ok := pc.Environment.Value.(string); ok && k != "" {
		return k, nil
	}
	return "", errors.New("no environment key — set FLAGSMITH_ENVIRONMENT_KEY or pass -e")
}

// apiRequestBody builds the request body (or query params) and resolves the
// method: explicit --method, else POST when a body/field is present, else GET.
func apiRequestBody(cmd *cobra.Command) (body []byte, contentType string, query url.Values, method string, err error) {
	hasFields := len(apiFieldFlags)+len(apiRawFields) > 0
	hasInput := cmd.Flags().Changed("input")
	if hasFields && hasInput {
		return nil, "", nil, "", usageErrorf("--input cannot be combined with -F/-f")
	}

	method = http.MethodGet
	if hasFields || hasInput {
		method = http.MethodPost
	}
	if cmd.Flags().Changed("method") {
		method = strings.ToUpper(apiMethodFlag)
	}

	switch {
	case hasInput:
		body, err = readInput(cmd, apiInputFlag)
		if err != nil {
			return nil, "", nil, "", err
		}
	case hasFields && method == http.MethodGet:
		fields, ferr := parseFields()
		if ferr != nil {
			return nil, "", nil, "", ferr
		}
		query = url.Values{}
		for k, v := range fields {
			query.Set(k, fmt.Sprint(v))
		}
	case hasFields:
		fields, ferr := parseFields()
		if ferr != nil {
			return nil, "", nil, "", ferr
		}
		body, err = json.Marshal(fields)
		if err != nil {
			return nil, "", nil, "", err
		}
		contentType = "application/json"
	}
	return body, contentType, query, method, nil
}

// parseFields merges typed (-F) and raw (-f) fields into one object.
func parseFields() (map[string]any, error) {
	fields := map[string]any{}
	for _, f := range apiFieldFlags {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return nil, usageErrorf("invalid field %q (want key=value)", f)
		}
		fields[k] = typedFieldValue(v)
	}
	for _, f := range apiRawFields {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return nil, usageErrorf("invalid raw field %q (want key=value)", f)
		}
		fields[k] = v
	}
	return fields, nil
}

// typedFieldValue infers a JSON type for a -F value: bool, null, number, else
// string.
func typedFieldValue(raw string) any {
	switch raw {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f
	}
	return raw
}

func parseHeaders(raw []string) (http.Header, error) {
	h := http.Header{}
	for _, item := range raw {
		k, v, ok := strings.Cut(item, ":")
		if !ok {
			return nil, usageErrorf("invalid header %q (want 'Name: value')", item)
		}
		h.Add(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	return h, nil
}

func readInput(cmd *cobra.Command, path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(cmd.InOrStdin())
	}
	return os.ReadFile(path)
}

// apiDo issues one request and reads the full response body.
func apiDo(ctx context.Context, method, u string, body []byte, contentType string, headers http.Header, applyAuth func(*http.Request)) (*http.Response, []byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return nil, nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for name, values := range headers {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	applyAuth(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	return resp, respBody, err
}

func statusOK(code int) bool { return code >= 200 && code < 300 }

// apiError prints the response body to stderr and returns a non-zero error.
func apiError(cmd *cobra.Command, method, u string, resp *http.Response, body []byte) error {
	errOut := cmd.ErrOrStderr()
	if len(body) > 0 {
		errOut.Write(body)
		if !bytes.HasSuffix(body, []byte("\n")) {
			fmt.Fprintln(errOut)
		}
	}
	return fmt.Errorf("%s %s returned %s", method, u, resp.Status)
}

func writeStatusLine(w io.Writer, resp *http.Response) {
	fmt.Fprintf(w, "%s %s\n", resp.Proto, resp.Status)
	_ = resp.Header.Write(w)
	fmt.Fprintln(w)
}

// writeBody writes the response verbatim, or filters it through jq. Raw
// passthrough preserves the API's own formatting; --jq requires JSON.
func writeBody(w io.Writer, body []byte, jqExpr string) error {
	if jqExpr == "" {
		_, err := w.Write(body)
		return err
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return fmt.Errorf("response is not JSON, cannot apply --jq: %w", err)
	}
	return output.Render(w, v, output.Options{JQ: jqExpr}, nil)
}

func init() {
	f := apiCmd.Flags()
	f.StringVarP(&apiMethodFlag, "method", "X", "GET", "HTTP method")
	f.StringArrayVarP(&apiFieldFlags, "field", "F", nil, "typed field key=value (repeatable)")
	f.StringArrayVarP(&apiRawFields, "raw-field", "f", nil, "string field key=value (repeatable)")
	f.StringVar(&apiInputFlag, "input", "", "request body from a file, or - for stdin")
	f.StringArrayVarP(&apiHeaderFlags, "header", "H", nil, "request header 'Name: value' (repeatable)")
	f.BoolVarP(&apiIncludeFlag, "include", "i", false, "include the response status line and headers")
	f.BoolVar(&apiSDKFlag, "sdk", false, "call the SDK API with the environment key instead of the Admin API")
	rootCmd.AddCommand(apiCmd)
}
