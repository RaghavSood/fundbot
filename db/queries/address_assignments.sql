-- name: GetAddressAssignment :one
SELECT id, assigned_to_id, assigned_to_type, created_at
FROM address_assignments
WHERE assigned_to_id = ? AND assigned_to_type = ?;

-- name: CreateAddressAssignment :one
INSERT INTO address_assignments (assigned_to_id, assigned_to_type)
VALUES (?, ?)
RETURNING id, assigned_to_id, assigned_to_type, created_at;

-- name: ListAddressAssignments :many
SELECT id, assigned_to_id, assigned_to_type, created_at
FROM address_assignments
ORDER BY id;
