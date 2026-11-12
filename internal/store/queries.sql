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
