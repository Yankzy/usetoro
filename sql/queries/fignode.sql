-- =========================================================================
-- Auth: Employee Registration & Login
-- =========================================================================

-- name: CreateEmployeeUser :one
INSERT INTO toro_core.users (entity_id, email, password_hash, role, user_type)
VALUES ($1, $2, $3, 'member', 'employee') RETURNING id;

-- name: CreateEmployeeProfile :exec
INSERT INTO fignode.employee_profiles (user_id, first_name, last_name, is_manager)
VALUES ($1, $2, $3, $4);

-- name: GetEmployeeByEmail :one
SELECT
    u.id,
    u.email,
    u.password_hash,
    u.entity_id,
    u.is_active,
    p.first_name,
    p.last_name,
    p.is_manager,
    p.ai_accuracy_score,
    p.streak,
    p.total_cleared,
    p.today_cleared,
    p.created_at
FROM toro_core.users u
JOIN fignode.employee_profiles p ON p.user_id = u.id
WHERE u.email = $1 AND u.user_type = 'employee';

-- name: GetEmployeeByID :one
SELECT
    u.id,
    u.email,
    u.entity_id,
    u.is_active,
    p.first_name,
    p.last_name,
    p.is_manager,
    p.ai_accuracy_score,
    p.streak,
    p.total_cleared,
    p.today_cleared,
    p.created_at
FROM toro_core.users u
JOIN fignode.employee_profiles p ON p.user_id = u.id
WHERE u.id = $1 AND u.user_type = 'employee';

-- name: GetEmployeePublic :one
SELECT
    u.id,
    u.email,
    p.first_name,
    p.last_name,
    p.is_manager,
    p.ai_accuracy_score,
    p.streak,
    p.total_cleared,
    p.today_cleared,
    p.created_at
FROM toro_core.users u
JOIN fignode.employee_profiles p ON p.user_id = u.id
WHERE u.id = $1 AND u.user_type = 'employee';

-- =========================================================================
-- Batch: Serving transactions to employees
-- =========================================================================

-- name: GetBatchTransactions :many
SELECT t.*
FROM fignode.transactions t
WHERE t.status = 'open'
  AND NOT EXISTS (
      SELECT 1 FROM fignode.classifications c
      WHERE c.transaction_id = t.id AND c.user_id = @user_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM fignode.skips s
      WHERE s.transaction_id = t.id AND s.user_id = @user_id
  )
ORDER BY t.created_at ASC
LIMIT @batch_limit;

-- name: InsertBatch :one
INSERT INTO fignode.batches (user_id, transaction_ids)
VALUES ($1, $2) RETURNING id;

-- =========================================================================
-- Classification: Categorizing transactions
-- =========================================================================

-- name: GetTransactionForClassify :one
SELECT * FROM fignode.transactions
WHERE id = $1 FOR UPDATE;

-- name: InsertClassification :one
INSERT INTO fignode.classifications (
    transaction_id, user_id, category, action, approved_by
) VALUES ($1, $2, $3, $4, $5)
RETURNING id;

-- name: CheckUserAlreadyClassified :one
SELECT EXISTS (
    SELECT 1 FROM fignode.classifications
    WHERE transaction_id = $1 AND user_id = $2
) AS already_classified;

-- name: ValidateCategory :one
SELECT EXISTS (
    SELECT 1 FROM fignode.categories WHERE label = $1
) AS is_valid;

-- =========================================================================
-- Status Updates
-- =========================================================================

-- name: ClearTransaction :exec
UPDATE fignode.transactions
SET status = 'cleared', cleared_at = now()
WHERE id = $1;

-- name: SetTransactionInReview :exec
UPDATE fignode.transactions
SET status = 'in_review'
WHERE id = $1;

-- name: IncrementEmployeeCleared :exec
UPDATE fignode.employee_profiles
SET total_cleared = total_cleared + 1,
    today_cleared = CASE
        WHEN today_cleared_date = CURRENT_DATE THEN today_cleared + 1
        ELSE 1
    END,
    today_cleared_date = CURRENT_DATE,
    updated_at = now()
WHERE user_id = $1;

-- =========================================================================
-- Stats: Employee dashboard
-- =========================================================================

-- name: GetEmployeeStats :one
SELECT
    p.total_cleared,
    p.today_cleared,
    p.streak,
    p.ai_accuracy_score
FROM fignode.employee_profiles p
WHERE p.user_id = $1;

-- =========================================================================
-- Leaderboard: Snapshot computation & retrieval
-- =========================================================================

-- name: ComputeAllTimeLeaderboard :many
SELECT
    p.user_id,
    u.email,
    p.total_cleared AS cleared,
    p.streak
FROM fignode.employee_profiles p
JOIN toro_core.users u ON u.id = p.user_id
WHERE u.is_active = TRUE
ORDER BY p.total_cleared DESC,
         p.streak DESC,
         u.email ASC
LIMIT 100;

-- name: ComputePeriodLeaderboard :many
SELECT
    p.user_id,
    u.email,
    COUNT(c.id)::INT AS cleared,
    p.streak
FROM fignode.employee_profiles p
JOIN toro_core.users u ON u.id = p.user_id
LEFT JOIN fignode.classifications c
    ON c.user_id = p.user_id AND c.created_at >= @since::TIMESTAMPTZ
WHERE u.is_active = TRUE
GROUP BY p.user_id, u.email, p.streak
ORDER BY COUNT(c.id) DESC,
         p.streak DESC,
         u.email ASC
LIMIT 100;

-- name: InsertLeaderboardSnapshot :exec
INSERT INTO fignode.leaderboard_snapshots (period, entries)
VALUES ($1, $2);

-- name: GetLatestLeaderboardSnapshot :one
SELECT entries, computed_at
FROM fignode.leaderboard_snapshots
WHERE period = $1
ORDER BY computed_at DESC
LIMIT 1;

-- =========================================================================
-- Streak: Update & midnight reset
-- =========================================================================

-- name: UpdateStreak :exec
UPDATE fignode.employee_profiles
SET streak = CASE
        WHEN streak_last_date IS NULL OR streak_last_date < CURRENT_DATE - INTERVAL '1 day'
            THEN 1
        WHEN streak_last_date = CURRENT_DATE - INTERVAL '1 day'
            THEN streak + 1
        ELSE streak
    END,
    streak_last_date = CURRENT_DATE,
    updated_at = now()
WHERE user_id = $1 AND (streak_last_date IS NULL OR streak_last_date < CURRENT_DATE);

-- name: ResetStaleStreaks :exec
UPDATE fignode.employee_profiles
SET streak = 0, updated_at = now()
WHERE streak > 0
  AND (streak_last_date IS NULL OR streak_last_date < CURRENT_DATE - INTERVAL '1 day');

-- name: ResetTodayCleared :exec
UPDATE fignode.employee_profiles
SET today_cleared = 0, today_cleared_date = CURRENT_DATE, updated_at = now()
WHERE today_cleared_date IS NULL OR today_cleared_date < CURRENT_DATE;

-- =========================================================================
-- Badges: Award & query
-- =========================================================================

-- name: UpsertBadge :exec
INSERT INTO fignode.badges (user_id, badge_key, badge_label)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, badge_key) DO NOTHING;

-- name: GetEmployeeBadges :many
SELECT badge_key, badge_label, earned_at
FROM fignode.badges
WHERE user_id = $1
ORDER BY earned_at ASC;

-- name: GetBadgesByUserIDs :many
SELECT user_id, badge_key, badge_label
FROM fignode.badges
WHERE user_id = ANY(@user_ids::uuid[]);

-- =========================================================================
-- Skip: Record a skip
-- =========================================================================

-- name: InsertSkip :exec
INSERT INTO fignode.skips (transaction_id, user_id)
VALUES ($1, $2)
ON CONFLICT (transaction_id, user_id) DO NOTHING;

-- =========================================================================
-- Updates
-- =========================================================================

-- name: GetEmployeeProfileForUpdate :one
SELECT * FROM fignode.employee_profiles
WHERE user_id = $1
FOR UPDATE;

-- name: AdjustAccuracyScore :exec
UPDATE fignode.employee_profiles
SET ai_accuracy_score = CASE
        WHEN @delta::NUMERIC > 0 THEN LEAST(1.000, ai_accuracy_score + @delta)
        ELSE GREATEST(0.000, ai_accuracy_score + @delta)
    END,
    updated_at = now()
WHERE user_id = @user_id;
