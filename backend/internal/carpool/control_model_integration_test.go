//go:build integration

package carpool_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/google/uuid"
	redisclient "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type productionModelNode struct {
	url   string
	start func()
	stop  func()
	cache *redisclient.Client
}

// The executable is an unchanged production build. Only management commands use
// the native TLS test router (positive fixtures do not accept legal declarations).
func startProductionModelNode(t *testing.T, remote nativeControlNode, mainID string, registered carpool.Node, binary, upstream, directory string) productionModelNode {
	t.Helper()
	ctx := context.Background()
	redis, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{
		Image: "redis:8.4-alpine", ExposedPorts: []string{"6379/tcp"}, Cmd: []string{"redis-server", "--save", "", "--appendonly", "no"}, WaitingFor: wait.ForListeningPort("6379/tcp"),
	}, Started: true})
	if redis != nil {
		t.Cleanup(func() { require.NoError(t, redis.Terminate(ctx)) })
	}
	require.NoError(t, err)
	redisHost, err := redis.Host(ctx)
	require.NoError(t, err)
	redisPort, err := redis.MappedPort(ctx, "6379/tcp")
	require.NoError(t, err)
	rdb := redisclient.NewClient(&redisclient.Options{Addr: net.JoinHostPort(redisHost, redisPort.Port())})
	t.Cleanup(func() { _ = rdb.Close() })
	dsn, err := url.Parse(remote.dsn)
	require.NoError(t, err)
	password, _ := dsn.User.Password()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	_, port, err := net.SplitHostPort(address)
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	require.NoError(t, os.MkdirAll(directory, 0700))
	credentials, err := json.Marshal(map[string]string{"api_key": "synthetic-upstream-" + registered.ID, "base_url": upstream})
	require.NoError(t, err)
	var account int64
	require.NoError(t, remote.db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,extra,concurrency,priority,status,schedulable)
 VALUES('Local model fixture','openai','apikey',$1,'{"openai_responses_supported":false}',4,1,'active',true) RETURNING id`, credentials).Scan(&account))
	_, err = remote.db.Exec(`INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,1)`, account, remote.groupID)
	require.NoError(t, err)
	env := map[string]string{
		"AUTO_SETUP": "true", "DATA_DIR": directory, "SERVER_HOST": "127.0.0.1", "SERVER_PORT": port, "SERVER_MODE": "debug", "SERVER_FRONTEND_URL": "http://" + address,
		"DATABASE_HOST": dsn.Hostname(), "DATABASE_PORT": dsn.Port(), "DATABASE_USER": dsn.User.Username(), "DATABASE_PASSWORD": password, "DATABASE_DBNAME": strings.TrimPrefix(dsn.Path, "/"), "DATABASE_SSLMODE": "disable",
		"REDIS_HOST": redisHost, "REDIS_PORT": redisPort.Port(), "REDIS_PASSWORD": "", "REDIS_DB": "0", "RUN_MODE": "standard", "TZ": "UTC",
		"ADMIN_EMAIL": remote.admin.Email, "ADMIN_PASSWORD": "Synthetic-" + uuid.NewString(), "JWT_SECRET": uuid.NewString() + uuid.NewString(),
		"TOTP_ENCRYPTION_KEY": strings.Repeat("4a", 32), "CARPOOL_NODE_ENABLED": "true", "CARPOOL_NODE_AUTHORIZATION_MODE": "managed",
		"CARPOOL_NODE_MAIN_INSTANCE_ID": mainID, "CARPOOL_NODE_NODE_ID": registered.ID, "CARPOOL_NODE_CAR_ID": registered.CarID, "CARPOOL_NODE_GROUP_ID": fmt.Sprint(remote.groupID),
		"PRICING_REMOTE_URL": upstream + "/prices", "PRICING_HASH_URL": upstream + "/prices.sha256", "PRICING_DATA_DIR": filepath.Join(directory, "pricing"),
		"BATCH_IMAGE_ENABLED": "false", "BATCH_IMAGE_VERTEX_ENABLED": "false", "HTTP_PROXY": "", "HTTPS_PROXY": "", "ALL_PROXY": "", "NO_PROXY": "*",
	}
	var child *exec.Cmd
	var exited chan error
	stop := func() {
		if child == nil {
			return
		}
		_ = child.Process.Signal(os.Interrupt)
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			_ = child.Process.Kill()
			<-exited
		}
		child = nil
	}
	t.Cleanup(stop)
	client := &http.Client{Timeout: time.Second}
	start := func() {
		log, e := os.OpenFile(filepath.Join(directory, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		require.NoError(t, e)
		// Only the local test operator supplies this absolute executable path; no HTTP input reaches it.
		child = exec.Command(binary) //nolint:gosec // G702: opted-in integration harness, no shell or untrusted arguments.
		child.Dir = directory
		child.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.TempDir()}
		for key, value := range env {
			child.Env = append(child.Env, key+"="+value)
		}
		child.Stdout, child.Stderr = log, log
		require.NoError(t, child.Start())
		require.NoError(t, log.Close())
		exited = make(chan error, 1)
		process := child
		go func() { exited <- process.Wait() }()
		require.Eventually(t, func() bool {
			response, e := client.Get("http://" + address + "/health")
			if e != nil {
				return false
			}
			_ = response.Body.Close()
			return response.StatusCode == 200
		}, 90*time.Second, 300*time.Millisecond, "model node must start; inspect private server.log")
	}
	start()
	return productionModelNode{url: "http://" + address, start: start, stop: stop, cache: rdb}
}

func TestControlProductionModelNodes(t *testing.T) {
	binary := os.Getenv("CARPOOL_MODEL_BINARY")
	if binary == "" {
		t.Skip("set CARPOOL_MODEL_BINARY to an embedded production server build")
	}
	require.True(t, filepath.IsAbs(binary))
	ctx := context.Background()
	directory, err := os.MkdirTemp("", "carpool-control-model-")
	require.NoError(t, err)
	fmt.Fprintln(os.Stdout, "Private model acceptance logs:", directory)
	var upstreamCount atomic.Int64
	streamDone := make(chan struct{})
	var finishStream sync.Once
	releaseStream := func() { finishStream.Do(func() { close(streamDone) }) }
	t.Cleanup(releaseStream)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			_, _ = io.WriteString(w, `{"gpt-4o-mini":{"input_cost_per_token":0.00000015,"output_cost_per_token":0.0000006,"mode":"chat"}}`)
			return
		}
		upstreamCount.Add(1)
		var input struct {
			Stream bool `json:"stream"`
		}
		if e := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&input); e != nil {
			w.WriteHeader(400)
			return
		}
		if input.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"local-stream\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"local \"},\"finish_reason\":null}]}\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			select {
			case <-streamDone:
			case <-r.Context().Done():
				return
			}
			_, _ = io.WriteString(w, "data: {\"id\":\"local-stream\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"managed stream fixture\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		_, _ = io.WriteString(w, `{"id":"chatcmpl-local","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"local managed model fixture"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`)
	}))
	t.Cleanup(upstream.Close)
	lab := newNativeControlLab(t)
	main := carpool.NewMainService(lab.db, carpool.MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	member := lab.user(t, "user")
	makeCar := func(host string) (carpool.Car, carpool.Node) {
		car, e := main.CreateCar(ctx, lab.admin.ID, carpool.CarInput{Name: host, Capacity: 2, PriceMinor: 100, Currency: "CNY", ServiceDays: 30, RecruitUntil: time.Now().Add(time.Hour), DeliveryHours: 24})
		require.NoError(t, e)
		_, e = main.PublishCar(ctx, lab.admin.ID, car.ID)
		require.NoError(t, e)
		_, e = main.EnrollManual(ctx, lab.admin.ID, car.ID, uuid.NewString(), carpool.ManualEnrollmentInput{UserID: member.ID, AmountMinor: 100, Channel: "alipay", ReceivedAt: time.Now().Add(-time.Minute), ConfirmReceived: true})
		require.NoError(t, e)
		node, e := main.RegisterNode(ctx, lab.admin.ID, carpool.NodeInput{CarID: car.ID, Hostname: host})
		require.NoError(t, e)
		return car, node
	}
	carB, registeredB := makeCar("b.model-lab.invalid")
	carC, registeredC := makeCar("c.model-lab.invalid")
	origins := []string{"https://" + registeredB.Hostname, "https://" + registeredC.Hostname}
	rootKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	store, err := carpool.NewControlStore(ctx, main, rootKey, origins)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	b := newNativeControlNode(t, store.MainID(), registeredB, roots)
	c := newNativeControlNode(t, store.MainID(), registeredC, roots)
	addresses := map[string]string{registeredB.Hostname + ":443": b.server.Listener.Addr().String(), registeredC.Hostname + ":443": c.server.Listener.Addr().String()}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		target, exists := addresses[address]
		if !exists {
			return nil, carpool.ErrControlTarget
		}
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	carpool.ConfigureTestTransport(store, transport)
	for _, pair := range []struct {
		registered carpool.Node
		remote     nativeControlNode
	}{{registeredB, b}, {registeredC, c}} {
		_, e := store.SaveConnection(ctx, lab.admin.ID, pair.registered.ID, carpool.ControlConnectionInput{Origin: "https://" + pair.registered.Hostname, InstanceID: pair.remote.node.Info().InstanceID, AdminKey: pair.remote.adminKey})
		require.NoError(t, e)
		_, e = store.CheckConnection(ctx, lab.admin.ID, pair.registered.ID)
		require.NoError(t, e)
	}
	drain := func() {
		require.NoError(t, store.ReconcileControl(ctx))
		for i := 0; i < 10; i++ {
			worked, e := store.SyncNext(ctx)
			require.NoError(t, e)
			if !worked {
				break
			}
		}
	}
	drain()
	_, err = store.CreateMemberKey(ctx, member.ID, carB.ID, uuid.NewString(), "unactivated")
	require.ErrorIs(t, err, carpool.ErrRequired)
	for _, remote := range []nativeControlNode{b, c} {
		var balance float64
		require.NoError(t, remote.db.QueryRow(`SELECT u.balance FROM users u JOIN carpool_managed_members m ON m.user_id=u.id LIMIT 1`).Scan(&balance))
		require.Zero(t, balance, "ordinary preparation cannot grant credit")
		// Explicit isolated phase-4 billing fixture, never a production grant path.
		_, err = remote.db.Exec(`UPDATE users SET balance=10 WHERE id IN (SELECT user_id FROM carpool_managed_members)`)
		require.NoError(t, err)
	}
	_, err = lab.db.Exec(`UPDATE carpool_cars SET status='active',service_start=clock_timestamp()-INTERVAL '1 hour',service_end=clock_timestamp()+INTERVAL '1 hour';
 UPDATE carpool_memberships m SET status='active',service_start=c.service_start,service_end=c.service_end FROM carpool_cars c WHERE c.id=m.car_id`)
	require.NoError(t, err)
	drain()
	keyB, err := store.CreateMemberKey(ctx, member.ID, carB.ID, uuid.NewString(), "B-model")
	require.NoError(t, err)
	keyC, err := store.CreateMemberKey(ctx, member.ID, carC.ID, uuid.NewString(), "C-model")
	require.NoError(t, err)
	modelB := startProductionModelNode(t, b, store.MainID(), registeredB, binary, upstream.URL, filepath.Join(directory, "b"))
	modelC := startProductionModelNode(t, c, store.MainID(), registeredC, binary, upstream.URL, filepath.Join(directory, "c"))
	client := &http.Client{Timeout: 30 * time.Second}
	call := func(site, key string, status int) {
		t.Helper()
		request, e := http.NewRequest(http.MethodPost, site+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"local-only fixture"}],"stream":false}`))
		require.NoError(t, e)
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("Content-Type", "application/json")
		result, e := client.Do(request)
		require.NoError(t, e)
		body, e := io.ReadAll(io.LimitReader(result.Body, 65536))
		_ = result.Body.Close()
		require.NoError(t, e)
		require.Equal(t, status, result.StatusCode, string(body))
		if status == 200 {
			require.Contains(t, string(body), "local managed model fixture")
		}
	}
	call(modelB.url, keyB.Key, 200)
	call(modelB.url, keyB.Key, 200)
	call(modelC.url, keyC.Key, 200)
	call(modelB.url, keyC.Key, 401)
	call(modelC.url, keyB.Key, 401)
	// Even a valid node-only test password must not create a managed member web session.
	nodePassword := "Synthetic-node-only-" + uuid.NewString()
	passwordHash, err := lab.auth.HashPassword(nodePassword)
	require.NoError(t, err)
	var internalEmail string
	require.NoError(t, b.db.QueryRow(`UPDATE users SET password_hash=$1 WHERE id=(SELECT user_id FROM api_keys WHERE id=$2) RETURNING email`, passwordHash, keyB.ID).Scan(&internalEmail))
	loginBody, err := json.Marshal(map[string]string{"email": internalEmail, "password": nodePassword})
	require.NoError(t, err)
	loginResult, err := client.Post(modelB.url+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	require.NoError(t, err)
	loginResponse, err := io.ReadAll(io.LimitReader(loginResult.Body, 65536))
	_ = loginResult.Body.Close()
	require.NoError(t, err)
	require.Equal(t, 403, loginResult.StatusCode, string(loginResponse))
	require.NotContains(t, string(loginResponse), "access_token")
	date := time.Now().UTC().Format(time.DateOnly)
	query := carpool.ControlUsageQuery{ControlPageQuery: carpool.ControlPageQuery{Page: 1, PageSize: 20}, StartDate: date, EndDate: date}
	require.Eventually(t, func() bool {
		stats, e := store.MemberUsageStats(ctx, member.ID, carB.ID, query)
		return e == nil && stats.TotalRequests == 2
	}, 30*time.Second, 200*time.Millisecond)
	stats, err := store.MemberUsageStats(ctx, member.ID, carC.ID, query)
	require.NoError(t, err)
	require.EqualValues(t, 1, stats.TotalRequests)
	records, err := store.MemberUsage(ctx, member.ID, carB.ID, query)
	require.NoError(t, err)
	require.Len(t, records.Items, 2)
	require.EqualValues(t, 11, records.Items[0].InputTokens)
	modelB.stop()
	modelB.start()
	call(modelB.url, keyB.Key, 200)
	store, err = carpool.NewControlStore(ctx, main, rootKey, origins)
	require.NoError(t, err)
	carpool.ConfigureTestTransport(store, transport)
	drain()
	call(modelB.url, keyB.Key, 200)
	streamRequest, err := http.NewRequest(http.MethodPost, modelB.url+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"local stream fixture"}],"stream":true}`))
	require.NoError(t, err)
	streamRequest.Header.Set("Authorization", "Bearer "+keyB.Key)
	streamRequest.Header.Set("Content-Type", "application/json")
	streamResponse, err := client.Do(streamRequest)
	require.NoError(t, err)
	defer func() { _ = streamResponse.Body.Close() }()
	require.Equal(t, 200, streamResponse.StatusCode)
	streamReader := bufio.NewReader(streamResponse.Body)
	firstLine, err := streamReader.ReadString('\n')
	require.NoError(t, err)
	require.Contains(t, firstLine, "data:")
	_, err = lab.db.Exec(`UPDATE carpool_memberships SET status='revoked' WHERE car_id=$1`, carB.ID)
	require.NoError(t, err)
	drain()
	before := upstreamCount.Load()
	call(modelB.url, keyB.Key, 403)
	require.Equal(t, before, upstreamCount.Load())
	releaseStream()
	streamTail, err := io.ReadAll(io.LimitReader(streamReader, 65536))
	require.NoError(t, err)
	require.Contains(t, string(streamTail), "managed stream fixture")
	require.Contains(t, string(streamTail), "[DONE]")
	call(modelC.url, keyC.Key, 200)
	_, err = lab.db.Exec(`UPDATE carpool_cars SET status='paused' WHERE id=$1`, carC.ID)
	require.NoError(t, err)
	drain()
	before = upstreamCount.Load()
	call(modelC.url, keyC.Key, 403)
	require.Equal(t, before, upstreamCount.Load())
	_, err = lab.db.Exec(`UPDATE carpool_cars SET status='active',service_end=clock_timestamp()+INTERVAL '3 seconds' WHERE id=$1`, carC.ID)
	require.NoError(t, err)
	_, err = lab.db.Exec(`UPDATE carpool_memberships m SET service_end=c.service_end FROM carpool_cars c WHERE c.id=m.car_id AND c.id=$1`, carC.ID)
	require.NoError(t, err)
	drain()
	call(modelC.url, keyC.Key, 200)
	require.Eventually(t, func() bool {
		var ended bool
		e := lab.db.QueryRow(`SELECT service_end<=clock_timestamp() FROM carpool_cars WHERE id=$1`, carC.ID).Scan(&ended)
		return e == nil && ended
	}, 5*time.Second, 100*time.Millisecond)
	before = upstreamCount.Load()
	call(modelC.url, keyC.Key, 403)
	require.Equal(t, before, upstreamCount.Load())
	_, err = lab.db.Exec(`UPDATE carpool_cars SET service_end=clock_timestamp()+INTERVAL '30 minutes' WHERE id=$1`, carC.ID)
	require.NoError(t, err)
	_, err = lab.db.Exec(`UPDATE carpool_memberships m SET service_end=c.service_end FROM carpool_cars c WHERE c.id=m.car_id AND c.id=$1`, carC.ID)
	require.NoError(t, err)
	drain()
	call(modelC.url, keyC.Key, 200)
	_, err = c.db.Exec(`UPDATE carpool_managed_members SET lease_until=clock_timestamp()-INTERVAL '1 second'`)
	require.NoError(t, err)
	before = upstreamCount.Load()
	call(modelC.url, keyC.Key, 403)
	require.Equal(t, before, upstreamCount.Load())
	fmt.Fprintln(os.Stdout, "PASS production managed B/C forwarding, native usage, key isolation, denied member login, node/store restart, revoke with in-flight stream completion, pause, service expiry and local lease expiry")
}
