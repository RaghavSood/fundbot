-- name: PruneAPIRequests :execrows
-- Deletes api_requests older than @cutoff EXCEPT rows worth retaining for audit:
--   1. Swap-creating POSTs: SimpleSwap create_exchange, Houdini /exchange,
--      confidential-intents submit-intent, and Near deposit notifications.
--   2. Completion-confirming responses: a status poll whose body shows a
--      terminal success state (SimpleSwap "finished", Near/Intents "SUCCESS",
--      Thorchain swap_finalised, Houdini status 4).
-- Everything else (quotes, balance/auth calls, and interim pending status
-- polls) is pruned once older than the cutoff.
DELETE FROM api_requests
WHERE created_at < @cutoff
  AND NOT (
    method = 'POST' AND (
      url LIKE '%create_exchange%'
      OR url LIKE '%/exchange%'
      OR url LIKE '%/submit-intent%'
      OR url LIKE '%/deposit/submit%'
    )
  )
  AND NOT (
    COALESCE(response_body, '') LIKE '%"status":"finished"%'
    OR COALESCE(response_body, '') LIKE '%"status":"SUCCESS"%'
    OR COALESCE(response_body, '') LIKE '%swap_finalised%'
    OR (url LIKE '%/status%' AND COALESCE(response_body, '') LIKE '%"status":4%')
  );
