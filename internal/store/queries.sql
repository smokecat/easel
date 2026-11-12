-- name: GetPrototypeByName :one
SELECT * FROM prototype WHERE name = ?;

-- name: GetPrototypeByLabel :one
SELECT * FROM prototype WHERE label = ?;

-- name: CreatePrototype :one
INSERT INTO prototype (name, label, source_fingerprint, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: SetCurrentRevision :exec
UPDATE prototype SET current_revision_id = ?, updated_at = ? WHERE id = ?;

-- name: GetRevision :one
SELECT * FROM revision WHERE id = ?;

-- name: GetRevisionByNumber :one
SELECT * FROM revision WHERE prototype_id = ? AND number = ?;

-- name: NextRevisionNumber :one
SELECT CAST(COALESCE(MAX(number), 0) + 1 AS INTEGER) AS next FROM revision WHERE prototype_id = ?;

-- name: CreateRevision :one
INSERT INTO revision (prototype_id, number, origin, kind, entry, spa, note, manifest_hash, file_count, total_bytes, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: CreateArtifact :exec
INSERT INTO artifact (revision_id, path, blob_hash, size, content_type)
VALUES (?, ?, ?, ?, ?);

-- name: ListArtifacts :many
SELECT * FROM artifact WHERE revision_id = ? ORDER BY path;

-- name: CreateUpload :exec
INSERT INTO upload (id, name, request, created_at, expires_at) VALUES (?, ?, ?, ?, ?);

-- name: GetUpload :one
SELECT * FROM upload WHERE id = ? AND expires_at > ?;

-- name: DeleteUpload :exec
DELETE FROM upload WHERE id = ?;

-- name: DeleteExpiredUploads :exec
DELETE FROM upload WHERE expires_at <= ?;

-- name: BlobReferenced :one
SELECT EXISTS (SELECT 1 FROM artifact WHERE blob_hash = ?) AS referenced;

-- name: GetAdmin :one
SELECT * FROM admin WHERE id = 1;

-- name: SetAdminPassword :exec
INSERT INTO admin (id, password_hash, updated_at) VALUES (1, ?, ?)
ON CONFLICT (id) DO UPDATE SET password_hash = excluded.password_hash, updated_at = excluded.updated_at;

-- name: CreateSession :exec
INSERT INTO session (id_hash, created_at, expires_at, user_agent) VALUES (?, ?, ?, ?);

-- name: GetSession :one
SELECT * FROM session WHERE id_hash = ? AND expires_at > ?;

-- name: DeleteSession :exec
DELETE FROM session WHERE id_hash = ?;

-- name: DeleteAllSessions :exec
DELETE FROM session;

-- name: CreateTicket :exec
INSERT INTO ticket (id_hash, host_label, session_hash, expires_at) VALUES (?, ?, ?, ?);

-- name: TakeTicket :one
DELETE FROM ticket WHERE id_hash = ? RETURNING *;

-- name: CreateViewSession :exec
INSERT INTO view_session (id_hash, host_label, session_hash, share_id, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetViewSession :one
SELECT * FROM view_session WHERE id_hash = ? AND host_label = ? AND expires_at > ?;

-- name: DeleteViewSession :exec
DELETE FROM view_session WHERE id_hash = ?;

-- name: CreateAPIToken :one
INSERT INTO api_token (name, token_hash, hint, scopes, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetAPITokenByHash :one
SELECT * FROM api_token WHERE token_hash = ?;

-- name: ListAPITokens :many
SELECT * FROM api_token ORDER BY id;

-- name: TouchAPIToken :exec
UPDATE api_token SET last_used_at = ? WHERE id = ?;

-- name: RevokeAPIToken :execrows
UPDATE api_token SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL;

-- name: CreateShare :one
INSERT INTO share (prototype_id, nonce, token_hash, note, password_hash, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetShare :one
SELECT * FROM share WHERE id = ?;

-- name: GetShareByTokenHash :one
SELECT * FROM share WHERE token_hash = ?;

-- name: ListShares :many
SELECT sqlc.embed(share), prototype.name AS prototype_name, prototype.label AS prototype_label
FROM share JOIN prototype ON prototype.id = share.prototype_id
WHERE sqlc.narg(prototype_id) IS NULL OR share.prototype_id = sqlc.narg(prototype_id)
ORDER BY share.id;

-- name: RevokeShare :execrows
UPDATE share SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL;

-- name: DeleteShareViewSessions :exec
DELETE FROM view_session WHERE share_id = ?;

-- name: GetPrototype :one
SELECT * FROM prototype WHERE id = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM session WHERE expires_at <= ?;

-- name: DeleteExpiredTickets :exec
DELETE FROM ticket WHERE expires_at <= ?;

-- name: DeleteExpiredViewSessions :exec
DELETE FROM view_session WHERE expires_at <= ?;

-- name: ListPrototypes :many
SELECT sqlc.embed(prototype),
       revision.number AS rev_number, revision.kind AS rev_kind, revision.note AS rev_note,
       revision.file_count AS rev_files, revision.total_bytes AS rev_bytes, revision.created_at AS rev_created_at,
       (SELECT COUNT(*) FROM revision r2 WHERE r2.prototype_id = prototype.id) AS version_count
FROM prototype LEFT JOIN revision ON revision.id = prototype.current_revision_id
ORDER BY prototype.updated_at DESC, prototype.id DESC;

-- name: CountRevisions :one
SELECT COUNT(*) FROM revision WHERE prototype_id = ?;

-- name: ClearCurrentRevision :exec
UPDATE prototype SET current_revision_id = NULL WHERE id = ?;

-- name: DeletePrototype :execrows
DELETE FROM prototype WHERE id = ?;

-- name: BlobInPendingUpload :one
SELECT EXISTS (SELECT 1 FROM upload WHERE instr(request, sqlc.arg(hash)) > 0) AS pending;
