-- name: GetDashboardCounts :one
SELECT
    (SELECT count(*) FROM waitlist_entries w JOIN jobs j ON j.id=w.job_id JOIN customers c ON c.id=j.customer_id
     WHERE w.removed_at IS NULL AND j.archived_at IS NULL AND c.archived_at IS NULL)::bigint AS waitlist_count,
    (SELECT count(*) FROM appointments a
     WHERE a.starts_at < sqlc.arg(day_end)::timestamptz AND a.ends_at > sqlc.arg(day_start)::timestamptz
       AND a.lifecycle_status IN ('proposal','fixed'))::bigint AS appointment_count,
    (SELECT count(*) FROM appointments a
     WHERE a.starts_at < sqlc.arg(horizon_end)::timestamptz AND a.ends_at > sqlc.arg(day_start)::timestamptz
       AND a.lifecycle_status='fixed' AND a.confirmation_status IN ('pending','declined','callback_requested'))::bigint AS attention_count,
    (SELECT count(*) FROM appointments a
       JOIN confirmation_requests cr ON cr.appointment_id=a.id AND cr.status='active'
     WHERE a.starts_at < sqlc.arg(horizon_end)::timestamptz AND a.ends_at > sqlc.arg(day_start)::timestamptz
       AND a.lifecycle_status='fixed' AND a.confirmation_status='pending'
       AND cr.created_at <= sqlc.arg(pending_before)::timestamptz)::bigint AS overdue_confirmation_count,
    (SELECT count(*) FROM appointments a
     WHERE a.starts_at < sqlc.arg(horizon_end)::timestamptz AND a.ends_at > sqlc.arg(day_start)::timestamptz
       AND a.lifecycle_status='fixed' AND a.confirmation_status='declined')::bigint AS declined_confirmation_count,
    (SELECT count(*) FROM appointments a
     WHERE a.starts_at < sqlc.arg(horizon_end)::timestamptz AND a.ends_at > sqlc.arg(day_start)::timestamptz
       AND a.lifecycle_status='fixed' AND a.confirmation_status='callback_requested')::bigint AS callback_request_count,
    (SELECT count(*) FROM waitlist_entries w JOIN jobs j ON j.id=w.job_id JOIN customers c ON c.id=j.customer_id
     WHERE w.removed_at IS NULL AND j.archived_at IS NULL AND c.archived_at IS NULL
       AND NOT EXISTS (SELECT 1 FROM appointments a WHERE a.job_id=j.id AND a.lifecycle_status IN ('proposal','fixed')))::bigint AS unplanned_count,
    (SELECT count(*) FROM notifications n
       JOIN confirmation_requests cr ON cr.id=n.confirmation_request_id AND cr.status='active'
       JOIN appointments a ON a.id=n.appointment_id AND a.lifecycle_status='fixed'
     WHERE n.status IN ('retry_wait','failed')
        OR (n.status IN ('queued','sending') AND n.updated_at <= sqlc.arg(pending_before)::timestamptz))::bigint AS notification_issue_count,
    (SELECT count(*) FROM appointments a
     WHERE a.starts_at < sqlc.arg(horizon_end)::timestamptz AND a.ends_at > sqlc.arg(day_start)::timestamptz
       AND a.lifecycle_status IN ('proposal','fixed') AND a.availability_override_reason IS NOT NULL)::bigint AS override_count,
    (SELECT count(*) FROM drivers d WHERE d.active)::bigint AS active_driver_count,
    (SELECT count(*) FROM voice_drafts v
     WHERE v.owner_user_id=sqlc.arg(owner_user_id)::uuid AND v.status='needs_review' AND v.expires_at>now())::bigint AS voice_draft_count;

-- name: ListDashboardAppointments :many
SELECT a.id::text, a.job_id::text, j.customer_id::text AS customer_id, j.job_number, a.lifecycle_status, a.confirmation_status,
       a.starts_at, a.ends_at, a.version, j.job_type, j.volume_m3::text,
       concat_ws(' ', NULLIF(c.first_name,''), NULLIF(c.last_name,''), NULLIF(c.company_name,''))::text AS customer_name,
       c.street, c.postal_code, c.locality, c.country_code,
       COALESCE(j.pile_latitude::text, '')::text AS latitude, COALESCE(j.pile_longitude::text, '')::text AS longitude,
       COALESCE(string_agg(DISTINCT d.display_name, ', ' ORDER BY d.display_name) FILTER (WHERE d.id IS NOT NULL), '')::text AS driver_names,
       COALESCE(string_agg(DISTINCT r.name, ', ' ORDER BY r.name) FILTER (WHERE r.id IS NOT NULL), '')::text AS resource_names,
       COALESCE(string_agg(DISTINCT r.name, ', ' ORDER BY r.name) FILTER (WHERE r.resource_type='chipper'), '')::text AS chipper_names,
       COALESCE((SELECT n.body FROM job_notes n WHERE n.job_id=j.id ORDER BY n.created_at DESC, n.id DESC LIMIT 1), '')::text AS latest_note,
       COALESCE(a.availability_override_reason, '')::text AS availability_override_reason
FROM appointments a
JOIN jobs j ON j.id=a.job_id
JOIN customers c ON c.id=j.customer_id
LEFT JOIN appointment_drivers ad ON ad.appointment_id=a.id
LEFT JOIN drivers d ON d.id=ad.driver_id
LEFT JOIN appointment_resources ar ON ar.appointment_id=a.id
LEFT JOIN resources r ON r.id=ar.resource_id
WHERE a.starts_at < sqlc.arg(range_end)::timestamptz AND a.ends_at > sqlc.arg(range_start)::timestamptz
  AND a.lifecycle_status IN ('proposal','fixed')
GROUP BY a.id, j.id, c.id
ORDER BY a.starts_at, a.id
LIMIT sqlc.arg(result_limit);

-- name: ListDashboardDriverAvailability :many
SELECT d.id::text, COALESCE(d.user_id::text, '')::text AS user_id, d.display_name,
       EXISTS (
         SELECT 1 FROM availability_rules r
         WHERE r.driver_id=d.id AND r.iso_weekday=sqlc.arg(iso_weekday)::smallint
           AND r.valid_from <= sqlc.arg(local_date)::date
           AND (r.valid_until IS NULL OR r.valid_until >= sqlc.arg(local_date)::date)
       ) AS has_rule,
       EXISTS (
         SELECT 1 FROM availability_rules r
         WHERE r.driver_id=d.id AND r.iso_weekday=sqlc.arg(iso_weekday)::smallint AND r.status='limited'
           AND r.valid_from <= sqlc.arg(local_date)::date
           AND (r.valid_until IS NULL OR r.valid_until >= sqlc.arg(local_date)::date)
       ) AS has_limited_rule,
       EXISTS (
         SELECT 1 FROM availability_exceptions e
         WHERE e.driver_id=d.id AND e.exception_type='available_override'
           AND ((e.all_day AND e.local_date=sqlc.arg(local_date)::date)
             OR (NOT e.all_day AND e.starts_at < sqlc.arg(day_end)::timestamptz AND e.ends_at > sqlc.arg(day_start)::timestamptz))
       ) AS has_available_override,
       EXISTS (
         SELECT 1 FROM availability_exceptions e
         WHERE e.driver_id=d.id AND e.exception_type<>'available_override'
           AND ((e.all_day AND e.local_date=sqlc.arg(local_date)::date)
             OR (NOT e.all_day AND e.starts_at < sqlc.arg(day_end)::timestamptz AND e.ends_at > sqlc.arg(day_start)::timestamptz))
       ) AS has_unavailable_exception,
       COALESCE((SELECT string_agg(to_char(r.local_start, 'HH24:MI') || '–' || to_char(r.local_end, 'HH24:MI'), ', ' ORDER BY r.local_start)
         FROM availability_rules r
         WHERE r.driver_id=d.id AND r.iso_weekday=sqlc.arg(iso_weekday)::smallint
           AND r.valid_from <= sqlc.arg(local_date)::date
           AND (r.valid_until IS NULL OR r.valid_until >= sqlc.arg(local_date)::date)), '')::text AS availability_windows,
       COALESCE((SELECT string_agg(DISTINCT CASE e.exception_type WHEN 'sick' THEN 'krank' WHEN 'vacation' THEN 'Urlaub' WHEN 'unavailable' THEN 'nicht verfügbar' ELSE 'Sonderverfügbarkeit' END, ', ')
         FROM availability_exceptions e
         WHERE e.driver_id=d.id AND ((e.all_day AND e.local_date=sqlc.arg(local_date)::date)
           OR (NOT e.all_day AND e.starts_at < sqlc.arg(day_end)::timestamptz AND e.ends_at > sqlc.arg(day_start)::timestamptz))), '')::text AS exception_reasons,
       COALESCE((SELECT round(sum(EXTRACT(EPOCH FROM (LEAST(a.ends_at, sqlc.arg(day_end)::timestamptz) - GREATEST(a.starts_at, sqlc.arg(day_start)::timestamptz))) / 60))::integer
         FROM appointment_drivers ad JOIN appointments a ON a.id=ad.appointment_id
         WHERE ad.driver_id=d.id AND a.lifecycle_status IN ('proposal','fixed')
           AND a.starts_at < sqlc.arg(day_end)::timestamptz AND a.ends_at > sqlc.arg(day_start)::timestamptz), 0)::integer AS booked_minutes
FROM drivers d
WHERE d.active
ORDER BY lower(d.display_name), d.id
LIMIT 200;

-- name: ListDashboardChipperBookings :many
SELECT r.id::text AS resource_id, r.name AS resource_name,
       ar.reserved_starts_at, ar.reserved_ends_at
FROM resources r
LEFT JOIN appointment_resources ar ON ar.resource_id=r.id AND ar.active
  AND ar.reserved_starts_at < sqlc.arg(capacity_end)::timestamptz
  AND ar.reserved_ends_at > sqlc.arg(capacity_start)::timestamptz
WHERE r.active AND r.resource_type='chipper'
ORDER BY lower(r.name), r.id, ar.reserved_starts_at NULLS LAST;

-- name: ListDashboardUrgentJobs :many
SELECT j.id::text, j.customer_id::text AS customer_id, j.job_number, j.urgency, j.volume_m3::text, j.received_at,
       j.preferred_end_date,
       concat_ws(' ', NULLIF(c.first_name,''), NULLIF(c.last_name,''), NULLIF(c.company_name,''))::text AS customer_name,
       c.locality
FROM waitlist_entries w
JOIN jobs j ON j.id=w.job_id
JOIN customers c ON c.id=j.customer_id
WHERE w.removed_at IS NULL AND j.archived_at IS NULL AND c.archived_at IS NULL
  AND (j.urgency IN ('high','urgent') OR j.received_at <= sqlc.arg(old_before)::timestamptz
       OR (j.preferred_end_date IS NOT NULL AND j.preferred_end_date <= sqlc.arg(preferred_before)::date))
ORDER BY array_position(ARRAY['urgent','high','normal','low'], j.urgency),
         j.preferred_end_date NULLS LAST, j.received_at, j.id
LIMIT sqlc.arg(result_limit);

-- name: ListDashboardUnplannedJobs :many
SELECT j.id::text, j.customer_id::text AS customer_id, j.job_number, j.urgency, j.volume_m3::text, j.received_at,
       j.preferred_end_date,
       concat_ws(' ', NULLIF(c.first_name,''), NULLIF(c.last_name,''), NULLIF(c.company_name,''))::text AS customer_name,
       c.locality
FROM waitlist_entries w
JOIN jobs j ON j.id=w.job_id
JOIN customers c ON c.id=j.customer_id
WHERE w.removed_at IS NULL AND j.archived_at IS NULL AND c.archived_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM appointments a WHERE a.job_id=j.id AND a.lifecycle_status IN ('proposal','fixed'))
ORDER BY w.manual_priority DESC,
         array_position(ARRAY['urgent','high','normal','low'], j.urgency),
         j.preferred_end_date NULLS LAST, w.entered_at, w.id
LIMIT sqlc.arg(result_limit);
