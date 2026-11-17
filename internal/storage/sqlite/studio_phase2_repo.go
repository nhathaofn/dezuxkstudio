package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"app/internal/apperr"
	"app/internal/domain/studio"
)

const jobCols = `id, project_id, episode_id, shot_id, asset_id, job_kind, provider_id, model_ref,
	idempotency_key, request_json, status, provider_job_id, submit_token_json, progress,
	attempts, max_attempts, next_poll_at, output_path, output_json, error_text,
	estimated_usd, actual_usd, created_at, updated_at, started_at, finished_at`

const pricingCols = `id, provider_id, model_id, capability, unit, unit_price_usd, currency, updated_at`

const costCols = `id, project_id, episode_id, shot_id, job_id, provider_id, model_id,
	capability, operation, input_units, output_units, unit, estimated_usd, actual_usd,
	status, metadata_json, created_at`

const versionCols = `id, project_id, episode_id, target_kind, target_id, version_num, media_path,
	source_node, prompt_used, model_ref, seed, cost_usd, qc_status, is_selected,
	metadata_json, created_at`

const qcCols = `id, project_id, episode_id, target_kind, target_id, media_version_id, media_path,
	verdict, overall_score, ffprobe_json, issues_json, vision_summary, vision_model_ref,
	sample_frames_json, created_at`

func scanJob(s rowScanner) (studio.Job, error) {
	var j studio.Job
	var kind, status string
	if err := s.Scan(
		&j.ID, &j.ProjectID, &j.EpisodeID, &j.ShotID, &j.AssetID, &kind, &j.ProviderID, &j.ModelRef,
		&j.IdempotencyKey, &j.RequestJSON, &status, &j.ProviderJobID, &j.SubmitTokenJSON, &j.Progress,
		&j.Attempts, &j.MaxAttempts, &j.NextPollAt, &j.OutputPath, &j.OutputJSON, &j.ErrorText,
		&j.EstimatedUSD, &j.ActualUSD, &j.CreatedAt, &j.UpdatedAt, &j.StartedAt, &j.FinishedAt,
	); err != nil {
		return studio.Job{}, err
	}
	j.JobKind = studio.JobKind(kind)
	j.Status = studio.JobStatus(status)
	return j, nil
}

func scanPricing(s rowScanner) (studio.PricingEntry, error) {
	var p studio.PricingEntry
	if err := s.Scan(&p.ID, &p.ProviderID, &p.ModelID, &p.Capability, &p.Unit, &p.UnitPriceUSD, &p.Currency, &p.UpdatedAt); err != nil {
		return studio.PricingEntry{}, err
	}
	return p, nil
}

func scanCost(s rowScanner) (studio.CostLedgerEntry, error) {
	var c studio.CostLedgerEntry
	var status string
	if err := s.Scan(
		&c.ID, &c.ProjectID, &c.EpisodeID, &c.ShotID, &c.JobID, &c.ProviderID, &c.ModelID,
		&c.Capability, &c.Operation, &c.InputUnits, &c.OutputUnits, &c.Unit, &c.EstimatedUSD,
		&c.ActualUSD, &status, &c.MetadataJSON, &c.CreatedAt,
	); err != nil {
		return studio.CostLedgerEntry{}, err
	}
	c.Status = studio.CostStatus(status)
	return c, nil
}

func scanMediaVersion(s rowScanner) (studio.MediaVersion, error) {
	var v studio.MediaVersion
	var kind, qc string
	var seed sql.NullInt64
	var selected int
	if err := s.Scan(
		&v.ID, &v.ProjectID, &v.EpisodeID, &kind, &v.TargetID, &v.VersionNum, &v.MediaPath,
		&v.SourceNode, &v.PromptUsed, &v.ModelRef, &seed, &v.CostUSD, &qc, &selected,
		&v.MetadataJSON, &v.CreatedAt,
	); err != nil {
		return studio.MediaVersion{}, err
	}
	v.TargetKind = studio.MediaTargetKind(kind)
	v.QCStatus = studio.QCStatus(qc)
	v.IsSelected = selected == 1
	if seed.Valid {
		val := int(seed.Int64)
		v.Seed = &val
	}
	return v, nil
}

func scanQCReport(s rowScanner) (studio.QCReport, error) {
	var r studio.QCReport
	var kind, verdict, issuesJSON, framesJSON string
	if err := s.Scan(
		&r.ID, &r.ProjectID, &r.EpisodeID, &kind, &r.TargetID, &r.MediaVersionID, &r.MediaPath,
		&verdict, &r.OverallScore, &r.FFprobeJSON, &issuesJSON, &r.VisionSummary, &r.VisionModelRef,
		&framesJSON, &r.CreatedAt,
	); err != nil {
		return studio.QCReport{}, err
	}
	r.TargetKind = studio.MediaTargetKind(kind)
	r.Verdict = studio.QCStatus(verdict)
	if issuesJSON != "" {
		_ = json.Unmarshal([]byte(issuesJSON), &r.Issues)
	}
	if r.Issues == nil {
		r.Issues = []studio.QCIssue{}
	}
	if framesJSON != "" {
		_ = json.Unmarshal([]byte(framesJSON), &r.SampleFrames)
	}
	if r.SampleFrames == nil {
		r.SampleFrames = []string{}
	}
	return r, nil
}

// CreateJob persists a new durable queue job, returning CodeConflict on duplicate idempotency_key.
func (r *StudioRepository) CreateJob(ctx context.Context, j studio.Job) (studio.Job, error) {
	if err := j.Validate(); err != nil {
		return studio.Job{}, apperr.New(apperr.CodeValidation, err.Error())
	}
	if j.ID == "" {
		j.ID = uuid.NewString()
	}
	now := time.Now().UnixMilli()
	if j.CreatedAt <= 0 {
		j.CreatedAt = now
	}
	j.UpdatedAt = now
	_, err := r.db.sql.ExecContext(ctx, `
		INSERT INTO studio_jobs (`+jobCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.ProjectID, j.EpisodeID, j.ShotID, j.AssetID, string(j.JobKind), j.ProviderID, j.ModelRef,
		j.IdempotencyKey, j.RequestJSON, string(j.Status), j.ProviderJobID, j.SubmitTokenJSON, j.Progress,
		j.Attempts, j.MaxAttempts, j.NextPollAt, j.OutputPath, j.OutputJSON, j.ErrorText,
		j.EstimatedUSD, j.ActualUSD, j.CreatedAt, j.UpdatedAt, j.StartedAt, j.FinishedAt,
	)
	if isConstraintErr(err) {
		return studio.Job{}, apperr.New(apperr.CodeConflict, fmt.Sprintf("Studio job idempotency_key %q already exists", j.IdempotencyKey))
	}
	if err != nil {
		return studio.Job{}, fmt.Errorf("sqlite: create studio job: %w", err)
	}
	return j, nil
}

// GetJob returns a job by ID.
func (r *StudioRepository) GetJob(ctx context.Context, id string) (studio.Job, error) {
	row := r.db.sql.QueryRowContext(ctx, `SELECT `+jobCols+` FROM studio_jobs WHERE id = ?`, strings.TrimSpace(id))
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return studio.Job{}, apperr.New(apperr.CodeNotFound, fmt.Sprintf("Studio job %q not found", id))
	}
	if err != nil {
		return studio.Job{}, fmt.Errorf("sqlite: get studio job: %w", err)
	}
	return j, nil
}

// GetJobByIdempotencyKey looks up an existing job by its idempotency key.
func (r *StudioRepository) GetJobByIdempotencyKey(ctx context.Context, key string) (studio.Job, error) {
	row := r.db.sql.QueryRowContext(ctx, `SELECT `+jobCols+` FROM studio_jobs WHERE idempotency_key = ?`, strings.TrimSpace(key))
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return studio.Job{}, apperr.New(apperr.CodeNotFound, fmt.Sprintf("Studio job with key %q not found", key))
	}
	if err != nil {
		return studio.Job{}, fmt.Errorf("sqlite: get studio job by key: %w", err)
	}
	return j, nil
}

// ListJobs returns jobs matching filter ordered newest-first.
func (r *StudioRepository) ListJobs(ctx context.Context, f studio.JobFilter) ([]studio.Job, error) {
	var conds []string
	var args []any
	if f.ProjectID != "" {
		conds = append(conds, "project_id = ?")
		args = append(args, f.ProjectID)
	}
	if f.EpisodeID != "" {
		conds = append(conds, "episode_id = ?")
		args = append(args, f.EpisodeID)
	}
	if f.ShotID != "" {
		conds = append(conds, "shot_id = ?")
		args = append(args, f.ShotID)
	}
	if f.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, string(f.Status))
	}
	if f.JobKind != "" {
		conds = append(conds, "job_kind = ?")
		args = append(args, string(f.JobKind))
	}
	q := `SELECT ` + jobCols + ` FROM studio_jobs`
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY created_at DESC, id ASC"
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	q += " LIMIT ?"
	args = append(args, limit)

	rows, err := r.db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list studio jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []studio.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan studio job: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ListRecoverableJobs returns non-terminal non-paused jobs for startup resume inspection.
func (r *StudioRepository) ListRecoverableJobs(ctx context.Context) ([]studio.Job, error) {
	rows, err := r.db.sql.QueryContext(ctx, `
		SELECT `+jobCols+` FROM studio_jobs
		WHERE status IN ('queued', 'submitted', 'polling')
		ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list recoverable studio jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []studio.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan recoverable studio job: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ClaimNextQueuedJob atomically moves the oldest queued job to submitted so only one worker submits it.
func (r *StudioRepository) ClaimNextQueuedJob(ctx context.Context, nowMs int64) (studio.Job, bool, error) {
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return studio.Job{}, false, fmt.Errorf("sqlite: begin claim queued job: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
		SELECT `+jobCols+` FROM studio_jobs
		WHERE status = 'queued'
		ORDER BY created_at ASC, id ASC
		LIMIT 1`)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return studio.Job{}, false, nil
	}
	if err != nil {
		return studio.Job{}, false, fmt.Errorf("sqlite: select queued job: %w", err)
	}
	j.Status = studio.JobSubmitted
	j.Attempts++
	j.UpdatedAt = nowMs
	if j.StartedAt == 0 {
		j.StartedAt = nowMs
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE studio_jobs
		SET status = ?, attempts = ?, started_at = ?, updated_at = ?
		WHERE id = ? AND status = 'queued'`,
		string(j.Status), j.Attempts, j.StartedAt, j.UpdatedAt, j.ID)
	if err != nil {
		return studio.Job{}, false, fmt.Errorf("sqlite: claim queued job: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return studio.Job{}, false, nil
	}
	if err := tx.Commit(); err != nil {
		return studio.Job{}, false, fmt.Errorf("sqlite: commit claim queued job: %w", err)
	}
	return j, true, nil
}

// ClaimNextPollJob atomically leases the next polling job whose next_poll_at <= nowMs.
func (r *StudioRepository) ClaimNextPollJob(ctx context.Context, nowMs, leaseMs int64) (studio.Job, bool, error) {
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return studio.Job{}, false, fmt.Errorf("sqlite: begin claim poll job: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
		SELECT `+jobCols+` FROM studio_jobs
		WHERE status = 'polling' AND provider_job_id <> '' AND next_poll_at <= ?
		ORDER BY next_poll_at ASC, created_at ASC, id ASC
		LIMIT 1`, nowMs)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return studio.Job{}, false, nil
	}
	if err != nil {
		return studio.Job{}, false, fmt.Errorf("sqlite: select poll job: %w", err)
	}
	if leaseMs <= 0 {
		leaseMs = 30_000
	}
	j.NextPollAt = nowMs + leaseMs
	j.UpdatedAt = nowMs
	res, err := tx.ExecContext(ctx, `
		UPDATE studio_jobs
		SET next_poll_at = ?, updated_at = ?
		WHERE id = ? AND status = 'polling'`, j.NextPollAt, j.UpdatedAt, j.ID)
	if err != nil {
		return studio.Job{}, false, fmt.Errorf("sqlite: lease poll job: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return studio.Job{}, false, nil
	}
	if err := tx.Commit(); err != nil {
		return studio.Job{}, false, fmt.Errorf("sqlite: commit claim poll job: %w", err)
	}
	return j, true, nil
}

// SetJobProviderSubmission persists provider_job_id and submit_token_json immediately after remote submit.
func (r *StudioRepository) SetJobProviderSubmission(ctx context.Context, id, providerJobID, submitTokenJSON string, status studio.JobStatus, nextPollAt int64) (studio.Job, error) {
	if !status.Valid() {
		return studio.Job{}, apperr.New(apperr.CodeValidation, fmt.Sprintf("invalid job status %q", status))
	}
	now := time.Now().UnixMilli()
	res, err := r.db.sql.ExecContext(ctx, `
		UPDATE studio_jobs
		SET provider_job_id = ?, submit_token_json = ?,
			status = CASE WHEN status IN ('canceled', 'paused') THEN status ELSE ? END,
			next_poll_at = ?, updated_at = ?
		WHERE id = ?`,
		providerJobID, submitTokenJSON, string(status), nextPollAt, now, strings.TrimSpace(id),
	)
	if err != nil {
		return studio.Job{}, fmt.Errorf("sqlite: set provider submission: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return studio.Job{}, apperr.New(apperr.CodeNotFound, fmt.Sprintf("Studio job %q not found", id))
	}
	return r.GetJob(ctx, id)
}

// UpdateJob saves mutable state/output fields of a studio job.
func (r *StudioRepository) UpdateJob(ctx context.Context, j studio.Job) (studio.Job, error) {
	if err := j.Validate(); err != nil {
		return studio.Job{}, apperr.New(apperr.CodeValidation, err.Error())
	}
	j.UpdatedAt = time.Now().UnixMilli()
	res, err := r.db.sql.ExecContext(ctx, `
		UPDATE studio_jobs SET
			status = ?, provider_job_id = ?, submit_token_json = ?, progress = ?,
			attempts = ?, max_attempts = ?, next_poll_at = ?, output_path = ?, output_json = ?,
			error_text = ?, estimated_usd = ?, actual_usd = ?, updated_at = ?, started_at = ?, finished_at = ?
		WHERE id = ?`,
		string(j.Status), j.ProviderJobID, j.SubmitTokenJSON, j.Progress,
		j.Attempts, j.MaxAttempts, j.NextPollAt, j.OutputPath, j.OutputJSON,
		j.ErrorText, j.EstimatedUSD, j.ActualUSD, j.UpdatedAt, j.StartedAt, j.FinishedAt, j.ID,
	)
	if err != nil {
		return studio.Job{}, fmt.Errorf("sqlite: update studio job: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return studio.Job{}, apperr.New(apperr.CodeNotFound, fmt.Sprintf("Studio job %q not found", j.ID))
	}
	return j, nil
}

// PauseEpisodeJobs pauses active queued/submitted/polling jobs for an episode (or entire project if episodeID == "").
func (r *StudioRepository) PauseEpisodeJobs(ctx context.Context, projectID, episodeID string) (int, error) {
	now := time.Now().UnixMilli()
	res, err := r.db.sql.ExecContext(ctx, `
		UPDATE studio_jobs
		SET status = 'paused', updated_at = ?
		WHERE project_id = ? AND (? = '' OR episode_id = ?) AND status IN ('queued', 'submitted', 'polling')`,
		now, strings.TrimSpace(projectID), strings.TrimSpace(episodeID), strings.TrimSpace(episodeID),
	)
	if err != nil {
		return 0, fmt.Errorf("sqlite: pause studio jobs: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ResumeEpisodeJobs resumes paused jobs; jobs with provider_job_id return to polling without re-submitting.
func (r *StudioRepository) ResumeEpisodeJobs(ctx context.Context, projectID, episodeID string) (int, error) {
	now := time.Now().UnixMilli()
	resPoll, err := r.db.sql.ExecContext(ctx, `
		UPDATE studio_jobs
		SET status = 'polling', next_poll_at = ?, updated_at = ?
		WHERE project_id = ? AND (? = '' OR episode_id = ?) AND status = 'paused' AND provider_job_id <> ''`,
		now, now, strings.TrimSpace(projectID), strings.TrimSpace(episodeID), strings.TrimSpace(episodeID),
	)
	if err != nil {
		return 0, fmt.Errorf("sqlite: resume polling studio jobs: %w", err)
	}
	resQueue, err := r.db.sql.ExecContext(ctx, `
		UPDATE studio_jobs
		SET status = 'queued', updated_at = ?
		WHERE project_id = ? AND (? = '' OR episode_id = ?) AND status = 'paused' AND provider_job_id = ''`,
		now, strings.TrimSpace(projectID), strings.TrimSpace(episodeID), strings.TrimSpace(episodeID),
	)
	if err != nil {
		return 0, fmt.Errorf("sqlite: resume queued studio jobs: %w", err)
	}
	n1, _ := resPoll.RowsAffected()
	n2, _ := resQueue.RowsAffected()
	return int(n1 + n2), nil
}

// CancelJob marks a non-succeeded job as canceled.
func (r *StudioRepository) CancelJob(ctx context.Context, id string) (studio.Job, error) {
	j, err := r.GetJob(ctx, id)
	if err != nil {
		return studio.Job{}, err
	}
	if j.Status == studio.JobSucceeded {
		return j, nil
	}
	now := time.Now().UnixMilli()
	j.Status = studio.JobCanceled
	j.UpdatedAt = now
	j.FinishedAt = now
	return r.UpdateJob(ctx, j)
}

// ListPricing returns all entries in studio_pricing_catalog.
func (r *StudioRepository) ListPricing(ctx context.Context) ([]studio.PricingEntry, error) {
	rows, err := r.db.sql.QueryContext(ctx, `
		SELECT `+pricingCols+` FROM studio_pricing_catalog
		ORDER BY capability ASC, provider_id ASC, model_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list pricing catalog: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []studio.PricingEntry{}
	for rows.Next() {
		p, err := scanPricing(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan pricing entry: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpsertPricing inserts or updates a pricing catalog rule by (provider_id, model_id, capability, unit).
func (r *StudioRepository) UpsertPricing(ctx context.Context, p studio.PricingEntry) (studio.PricingEntry, error) {
	if strings.TrimSpace(p.ProviderID) == "" {
		p.ProviderID = "*"
	}
	if strings.TrimSpace(p.ModelID) == "" {
		p.ModelID = "*"
	}
	if strings.TrimSpace(p.Currency) == "" {
		p.Currency = "USD"
	}
	if strings.TrimSpace(p.Capability) == "" || strings.TrimSpace(p.Unit) == "" {
		return studio.PricingEntry{}, apperr.New(apperr.CodeValidation, "capability and unit are required")
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	p.UpdatedAt = time.Now().UnixMilli()
	_, err := r.db.sql.ExecContext(ctx, `
		INSERT INTO studio_pricing_catalog (`+pricingCols+`)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(provider_id, model_id, capability, unit) DO UPDATE SET
			unit_price_usd = excluded.unit_price_usd,
			currency = excluded.currency,
			updated_at = excluded.updated_at`,
		p.ID, p.ProviderID, p.ModelID, p.Capability, p.Unit, p.UnitPriceUSD, p.Currency, p.UpdatedAt,
	)
	if err != nil {
		return studio.PricingEntry{}, fmt.Errorf("sqlite: upsert pricing entry: %w", err)
	}
	return p, nil
}

// ResolveUnitPrice finds the most specific matching rate across exact, provider-wildcard, and global-wildcard rows.
func (r *StudioRepository) ResolveUnitPrice(ctx context.Context, providerID, modelID, capability, unit string) (studio.PricingEntry, bool, error) {
	prov := strings.ToLower(strings.TrimSpace(providerID))
	mod := strings.TrimSpace(modelID)
	capName := strings.ToLower(strings.TrimSpace(capability))
	u := strings.ToLower(strings.TrimSpace(unit))

	query := `
		SELECT ` + pricingCols + ` FROM studio_pricing_catalog
		WHERE capability = ? AND (? = '' OR unit = ?)
			AND (provider_id = ? OR provider_id = '*')
			AND (model_id = ? OR model_id = '*')
		ORDER BY
			CASE WHEN provider_id = ? THEN 0 ELSE 1 END,
			CASE WHEN model_id = ? THEN 0 ELSE 1 END,
			updated_at DESC
		LIMIT 1`
	row := r.db.sql.QueryRowContext(ctx, query, capName, u, u, prov, mod, prov, mod)
	p, err := scanPricing(row)
	if errors.Is(err, sql.ErrNoRows) {
		return studio.PricingEntry{}, false, nil
	}
	if err != nil {
		return studio.PricingEntry{}, false, fmt.Errorf("sqlite: resolve unit price: %w", err)
	}
	return p, true, nil
}

// RecordCost inserts an entry into studio_cost_ledger.
func (r *StudioRepository) RecordCost(ctx context.Context, c studio.CostLedgerEntry) (studio.CostLedgerEntry, error) {
	if strings.TrimSpace(c.ProviderID) == "" || strings.TrimSpace(c.Capability) == "" || strings.TrimSpace(c.Unit) == "" {
		return studio.CostLedgerEntry{}, apperr.New(apperr.CodeValidation, "provider_id, capability, and unit are required")
	}
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.Status == "" {
		c.Status = studio.CostRecorded
	}
	if c.MetadataJSON == "" {
		c.MetadataJSON = "{}"
	}
	if c.CreatedAt <= 0 {
		c.CreatedAt = time.Now().UnixMilli()
	}
	_, err := r.db.sql.ExecContext(ctx, `
		INSERT INTO studio_cost_ledger (`+costCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.ProjectID, c.EpisodeID, c.ShotID, c.JobID, c.ProviderID, c.ModelID,
		c.Capability, c.Operation, c.InputUnits, c.OutputUnits, c.Unit, c.EstimatedUSD,
		c.ActualUSD, string(c.Status), c.MetadataJSON, c.CreatedAt,
	)
	if err != nil {
		return studio.CostLedgerEntry{}, fmt.Errorf("sqlite: record studio cost: %w", err)
	}
	return c, nil
}

// ListCosts queries ledger rows matching filter newest-first.
func (r *StudioRepository) ListCosts(ctx context.Context, f studio.CostFilter) ([]studio.CostLedgerEntry, error) {
	var conds []string
	var args []any
	if f.ProjectID != "" {
		conds = append(conds, "project_id = ?")
		args = append(args, f.ProjectID)
	}
	if f.EpisodeID != "" {
		conds = append(conds, "episode_id = ?")
		args = append(args, f.EpisodeID)
	}
	if f.JobID != "" {
		conds = append(conds, "job_id = ?")
		args = append(args, f.JobID)
	}
	q := `SELECT ` + costCols + ` FROM studio_cost_ledger`
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY created_at DESC, id ASC"
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	q += " LIMIT ?"
	args = append(args, limit)

	rows, err := r.db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list studio costs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []studio.CostLedgerEntry{}
	for rows.Next() {
		c, err := scanCost(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan studio cost: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SummarizeCosts computes project/episode spend breakdown by capability and provider.
func (r *StudioRepository) SummarizeCosts(ctx context.Context, projectID, episodeID string) (studio.CostSummary, error) {
	sum := studio.CostSummary{
		ProjectID:    projectID,
		EpisodeID:    episodeID,
		Currency:     "USD",
		ByCapability: map[string]float64{},
		ByProvider:   map[string]float64{},
	}
	var conds []string
	var args []any
	if projectID != "" {
		conds = append(conds, "project_id = ?")
		args = append(args, projectID)
	}
	if episodeID != "" {
		conds = append(conds, "episode_id = ?")
		args = append(args, episodeID)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	rows, err := r.db.sql.QueryContext(ctx, `
		SELECT capability, provider_id, estimated_usd, actual_usd
		FROM studio_cost_ledger`+where, args...)
	if err != nil {
		return sum, fmt.Errorf("sqlite: summarize studio costs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var capName, prov string
		var est, act float64
		if err := rows.Scan(&capName, &prov, &est, &act); err != nil {
			return sum, fmt.Errorf("sqlite: scan cost summary row: %w", err)
		}
		sum.EntryCount++
		sum.EstimatedTotalUSD += est
		sum.ActualTotalUSD += act
		val := act
		if val == 0 {
			val = est
		}
		sum.ByCapability[capName] += val
		sum.ByProvider[prov] += val
	}
	return sum, rows.Err()
}

// CreateMediaVersion atomically assigns the next version_num for (target_kind, target_id) and persists it.
func (r *StudioRepository) CreateMediaVersion(ctx context.Context, v studio.MediaVersion) (studio.MediaVersion, error) {
	if !v.TargetKind.Valid() || strings.TrimSpace(v.TargetID) == "" || strings.TrimSpace(v.ProjectID) == "" {
		return studio.MediaVersion{}, apperr.New(apperr.CodeValidation, "valid target_kind, target_id, and project_id are required")
	}
	if strings.TrimSpace(v.MediaPath) == "" {
		return studio.MediaVersion{}, apperr.New(apperr.CodeValidation, "media_path is required")
	}
	if v.ID == "" {
		v.ID = uuid.NewString()
	}
	if v.QCStatus == "" {
		v.QCStatus = studio.QCPending
	}
	if v.MetadataJSON == "" {
		v.MetadataJSON = "{}"
	}
	if v.CreatedAt <= 0 {
		v.CreatedAt = time.Now().UnixMilli()
	}

	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return studio.MediaVersion{}, fmt.Errorf("sqlite: begin create version: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if v.VersionNum <= 0 {
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(version_num), 0) + 1
			FROM studio_media_versions
			WHERE target_kind = ? AND target_id = ?`, string(v.TargetKind), v.TargetID).Scan(&v.VersionNum); err != nil {
			return studio.MediaVersion{}, fmt.Errorf("sqlite: next version_num: %w", err)
		}
	}
	if v.IsSelected {
		if _, err := tx.ExecContext(ctx, `
			UPDATE studio_media_versions SET is_selected = 0
			WHERE target_kind = ? AND target_id = ?`, string(v.TargetKind), v.TargetID); err != nil {
			return studio.MediaVersion{}, fmt.Errorf("sqlite: clear selected version: %w", err)
		}
	}
	var seedVal any
	if v.Seed != nil {
		seedVal = *v.Seed
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO studio_media_versions (`+versionCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.ID, v.ProjectID, v.EpisodeID, string(v.TargetKind), v.TargetID, v.VersionNum, v.MediaPath,
		v.SourceNode, v.PromptUsed, v.ModelRef, seedVal, v.CostUSD, string(v.QCStatus),
		boolInt(v.IsSelected), v.MetadataJSON, v.CreatedAt,
	)
	if err != nil {
		return studio.MediaVersion{}, fmt.Errorf("sqlite: insert media version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return studio.MediaVersion{}, fmt.Errorf("sqlite: commit media version: %w", err)
	}
	return v, nil
}

// ListMediaVersions returns versions for a target ordered newest version first.
func (r *StudioRepository) ListMediaVersions(ctx context.Context, targetKind studio.MediaTargetKind, targetID string) ([]studio.MediaVersion, error) {
	rows, err := r.db.sql.QueryContext(ctx, `
		SELECT `+versionCols+` FROM studio_media_versions
		WHERE target_kind = ? AND target_id = ?
		ORDER BY version_num DESC, created_at DESC`, string(targetKind), strings.TrimSpace(targetID))
	if err != nil {
		return nil, fmt.Errorf("sqlite: list media versions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []studio.MediaVersion{}
	for rows.Next() {
		v, err := scanMediaVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan media version: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetMediaVersion fetches a version by ID.
func (r *StudioRepository) GetMediaVersion(ctx context.Context, id string) (studio.MediaVersion, error) {
	row := r.db.sql.QueryRowContext(ctx, `SELECT `+versionCols+` FROM studio_media_versions WHERE id = ?`, strings.TrimSpace(id))
	v, err := scanMediaVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return studio.MediaVersion{}, apperr.New(apperr.CodeNotFound, fmt.Sprintf("Studio media version %q not found", id))
	}
	if err != nil {
		return studio.MediaVersion{}, fmt.Errorf("sqlite: get media version: %w", err)
	}
	return v, nil
}

// SelectMediaVersion atomically marks one version active and unselects its siblings.
func (r *StudioRepository) SelectMediaVersion(ctx context.Context, id string) (studio.MediaVersion, error) {
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return studio.MediaVersion{}, fmt.Errorf("sqlite: begin select version: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `SELECT `+versionCols+` FROM studio_media_versions WHERE id = ?`, strings.TrimSpace(id))
	v, err := scanMediaVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return studio.MediaVersion{}, apperr.New(apperr.CodeNotFound, fmt.Sprintf("Studio media version %q not found", id))
	}
	if err != nil {
		return studio.MediaVersion{}, fmt.Errorf("sqlite: select version fetch: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE studio_media_versions SET is_selected = 0 WHERE target_kind = ? AND target_id = ?`,
		string(v.TargetKind), v.TargetID); err != nil {
		return studio.MediaVersion{}, fmt.Errorf("sqlite: clear active version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE studio_media_versions SET is_selected = 1 WHERE id = ?`, v.ID); err != nil {
		return studio.MediaVersion{}, fmt.Errorf("sqlite: set active version: %w", err)
	}
	v.IsSelected = true
	if err := tx.Commit(); err != nil {
		return studio.MediaVersion{}, fmt.Errorf("sqlite: commit select version: %w", err)
	}
	return v, nil
}

// UpdateMediaVersionQC sets qc_status on a media version.
func (r *StudioRepository) UpdateMediaVersionQC(ctx context.Context, id string, status studio.QCStatus) error {
	if !status.Valid() {
		return apperr.New(apperr.CodeValidation, fmt.Sprintf("invalid qc status %q", status))
	}
	res, err := r.db.sql.ExecContext(ctx, `UPDATE studio_media_versions SET qc_status = ? WHERE id = ?`, string(status), strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("sqlite: update media version qc: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.New(apperr.CodeNotFound, fmt.Sprintf("Studio media version %q not found", id))
	}
	return nil
}

// CreateQCReport persists a multimodal Visual QC report and updates the linked version's qc_status if provided.
func (r *StudioRepository) CreateQCReport(ctx context.Context, rep studio.QCReport) (studio.QCReport, error) {
	if !rep.TargetKind.Valid() || strings.TrimSpace(rep.TargetID) == "" || strings.TrimSpace(rep.ProjectID) == "" {
		return studio.QCReport{}, apperr.New(apperr.CodeValidation, "valid target_kind, target_id, and project_id are required")
	}
	if !rep.Verdict.Valid() {
		return studio.QCReport{}, apperr.New(apperr.CodeValidation, fmt.Sprintf("invalid qc verdict %q", rep.Verdict))
	}
	if rep.ID == "" {
		rep.ID = uuid.NewString()
	}
	if rep.CreatedAt <= 0 {
		rep.CreatedAt = time.Now().UnixMilli()
	}
	if rep.FFprobeJSON == "" {
		rep.FFprobeJSON = "{}"
	}
	issuesBytes, _ := json.Marshal(rep.Issues)
	framesBytes, _ := json.Marshal(rep.SampleFrames)

	_, err := r.db.sql.ExecContext(ctx, `
		INSERT INTO studio_qc_reports (`+qcCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rep.ID, rep.ProjectID, rep.EpisodeID, string(rep.TargetKind), rep.TargetID, rep.MediaVersionID,
		rep.MediaPath, string(rep.Verdict), rep.OverallScore, rep.FFprobeJSON, string(issuesBytes),
		rep.VisionSummary, rep.VisionModelRef, string(framesBytes), rep.CreatedAt,
	)
	if err != nil {
		return studio.QCReport{}, fmt.Errorf("sqlite: create qc report: %w", err)
	}
	if rep.MediaVersionID != "" {
		_ = r.UpdateMediaVersionQC(ctx, rep.MediaVersionID, rep.Verdict)
	}
	return rep, nil
}

// ListQCReports returns QC reports for a target ordered newest-first.
func (r *StudioRepository) ListQCReports(ctx context.Context, targetKind studio.MediaTargetKind, targetID string) ([]studio.QCReport, error) {
	rows, err := r.db.sql.QueryContext(ctx, `
		SELECT `+qcCols+` FROM studio_qc_reports
		WHERE target_kind = ? AND target_id = ?
		ORDER BY created_at DESC, id ASC`, string(targetKind), strings.TrimSpace(targetID))
	if err != nil {
		return nil, fmt.Errorf("sqlite: list qc reports: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []studio.QCReport{}
	for rows.Next() {
		rep, err := scanQCReport(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan qc report: %w", err)
		}
		out = append(out, rep)
	}
	return out, rows.Err()
}

// GetLatestQCReport returns the most recent QC report for a target.
func (r *StudioRepository) GetLatestQCReport(ctx context.Context, targetKind studio.MediaTargetKind, targetID string) (studio.QCReport, error) {
	row := r.db.sql.QueryRowContext(ctx, `
		SELECT `+qcCols+` FROM studio_qc_reports
		WHERE target_kind = ? AND target_id = ?
		ORDER BY created_at DESC, id ASC LIMIT 1`, string(targetKind), strings.TrimSpace(targetID))
	rep, err := scanQCReport(row)
	if errors.Is(err, sql.ErrNoRows) {
		return studio.QCReport{}, apperr.New(apperr.CodeNotFound, fmt.Sprintf("QC report for %s/%s not found", targetKind, targetID))
	}
	if err != nil {
		return studio.QCReport{}, fmt.Errorf("sqlite: get latest qc report: %w", err)
	}
	return rep, nil
}
