package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	syncpg "tripfolio/server/internal/adapters/postgres/sync"
	"tripfolio/server/internal/adapters/security"
	"tripfolio/server/internal/config"
	syncmodule "tripfolio/server/internal/modules/sync"
	"tripfolio/server/internal/web"
)

var errSyncCLI = errors.New("同步维护操作失败，请核对参数、持久密钥、数据库和发布验收资料")

func RunSyncReleaseInfo(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 1 || args[0] != "release-info" {
		return errSyncCLI
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		return errSyncCLI
	}
	assets, err := web.Assets()
	if err != nil {
		return errSyncCLI
	}
	facts, err := releaseFacts(ctx, executable, assets)
	if err != nil {
		return errSyncCLI
	}
	if json.NewEncoder(out).Encode(facts) != nil {
		return errSyncCLI
	}
	return nil
}

type activationArguments struct {
	owner, snapshot uuid.UUID
	proofFile       string
}

func parseActivationArguments(args []string) (activationArguments, error) {
	var out activationArguments
	if len(args) != 7 || args[0] != "activate" {
		return out, errSyncCLI
	}
	values := map[string]string{}
	for i := 1; i < len(args); i += 2 {
		key := args[i]
		if key != "--account" && key != "--snapshot" && key != "--proof-file" {
			return out, errSyncCLI
		}
		if _, ok := values[key]; ok || args[i+1] == "" {
			return out, errSyncCLI
		}
		values[key] = args[i+1]
	}
	var err error
	if out.owner, err = uuid.Parse(values["--account"]); err != nil || out.owner == uuid.Nil {
		return out, errSyncCLI
	}
	if out.snapshot, err = uuid.Parse(values["--snapshot"]); err != nil || out.snapshot == uuid.Nil {
		return out, errSyncCLI
	}
	out.proofFile = values["--proof-file"]
	if out.proofFile == "" {
		return out, errSyncCLI
	}
	return out, nil
}

func readActivationProof(ctx context.Context, path string, in io.Reader) (string, error) {
	reader := in
	if path != "-" {
		st, err := os.Lstat(path)
		if err != nil || !st.Mode().IsRegular() || st.Size() > 8192 {
			return "", errSyncCLI
		}
		file, err := os.Open(path)
		if err != nil {
			return "", errSyncCLI
		}
		defer file.Close()
		reader = file
	}
	if reader == nil {
		return "", errSyncCLI
	}
	raw, err := io.ReadAll(io.LimitReader(contextReader{ctx, reader}, 8193))
	if err != nil || len(raw) > 8192 {
		return "", errSyncCLI
	}
	proof := strings.TrimRight(string(raw), "\r\n")
	if proof == "" || strings.ContainsAny(proof, " \t\r\n") {
		return "", errSyncCLI
	}
	return proof, nil
}

func RunSyncCLI(ctx context.Context, cfg config.Config, args []string, in io.Reader, out io.Writer) error {
	parsed, err := parseActivationArguments(args)
	if err != nil {
		return errSyncCLI
	}
	if strings.TrimSpace(cfg.Keyring) == "" {
		return errSyncCLI
	}
	keys, err := security.ParseKeyring(cfg.Keyring)
	if err != nil {
		return errSyncCLI
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	proof, err := readActivationProof(ctx, parsed.proofFile, in)
	if err != nil {
		return errSyncCLI
	}
	// Fail before opening the database if this executable lacks release evidence.
	verifier := syncReleaseVerifier{}
	if verifier.Verify(ctx) != nil {
		return errSyncCLI
	}
	pool, err := pgcore.NewPool(ctx, cfg.DatabaseURL, 2)
	if err != nil {
		return errSyncCLI
	}
	defer pool.Close()
	ready, err := newDBReadiness(pool)
	if err != nil || ready.Check(ctx) != nil {
		return errSyncCLI
	}
	service := syncmodule.NewActivationService(syncpg.NewActivationStore(pool), security.NewCursorCodec(keys), verifier)
	result, err := service.Activate(ctx, parsed.owner, parsed.snapshot, proof)
	if err != nil {
		return errSyncCLI
	}
	response := struct {
		AccountID uuid.UUID `json:"account_id"`
		SyncEpoch uuid.UUID `json:"sync_epoch"`
		EnabledAt time.Time `json:"enabled_at"`
	}{result.AccountID, result.SyncEpoch, result.EnabledAt}
	if json.NewEncoder(out).Encode(response) != nil {
		return errSyncCLI
	}
	return nil
}
