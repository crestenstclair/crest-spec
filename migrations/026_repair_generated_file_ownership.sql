-- Restore generated-file ownership that was lost when an accepted update
-- replaced a resource's complete ownership set with only the files present in
-- that candidate. Candidate file acceptances are immutable provenance, so
-- they are the authoritative source for this repair.
--
-- Existing ownership rows win. For a missing (path, resource_id) pair, use
-- the latest accepted candidate record so its content and execution metadata
-- describe the most recently accepted version of that file.
WITH ranked_acceptances AS (
    SELECT
        cfa.path,
        cfa.resource_id,
        cf.content_hash,
        em.context_hash AS prompt_hash,
        em.model,
        cfa.accepted_at AS created_at,
        ROW_NUMBER() OVER (
            PARTITION BY cfa.resource_id, cfa.path
            ORDER BY cfa.accepted_at DESC, cfa.candidate_file_id DESC
        ) AS recency_rank
    FROM candidate_file_acceptances AS cfa
    JOIN resources AS r
      ON r.id = cfa.resource_id
    JOIN candidate_files AS cf
      ON cf.id = cfa.candidate_file_id
     AND cf.path = cfa.path
    JOIN candidate_sets AS cs
      ON cs.id = cf.candidate_id
     AND cs.status = 'accepted'
    JOIN execution_manifests AS em
      ON em.id = cs.execution_id
     AND em.status = 'accepted'
)
INSERT OR IGNORE INTO generated_files (
    path, resource_id, content_hash, prompt_hash, model, created_at
)
SELECT
    path, resource_id, content_hash, prompt_hash, model, created_at
FROM ranked_acceptances
WHERE recency_rank = 1;
