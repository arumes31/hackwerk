-- name: ListActiveTransportPartners :many
SELECT id::text, partner_type, name, COALESCE(phone, '')::text AS phone,
       COALESCE(address, '')::text AS address, COALESCE(internal_note, '')::text AS internal_note,
       version, created_at, updated_at
FROM transport_partners
WHERE active
ORDER BY lower(name), id;

-- name: InsertTransportPartner :one
INSERT INTO transport_partners (partner_type, name, phone, address, internal_note)
VALUES (sqlc.arg(partner_type), sqlc.arg(name), NULLIF(sqlc.arg(phone)::text, ''),
        NULLIF(sqlc.arg(address)::text, ''), NULLIF(sqlc.arg(internal_note)::text, ''))
RETURNING id::text;

-- name: LockActiveTransportPartner :one
SELECT id::text
FROM transport_partners
WHERE id=sqlc.arg(id)::uuid AND active
FOR SHARE;
