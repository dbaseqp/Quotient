# PostgreSQL Materialized Views Implementation Summary

## Overview
Implemented PostgreSQL materialized views to optimize graph data queries, specifically the cumulative scores calculation that previously ran expensive window functions on every API call.

## Changes Made

### 1. Materialized View Creation (`engine/db/db.go`)
- Added creation of `cumulative_scores` materialized view in the `Connect()` function
- The view pre-computes cumulative points per team per round using window functions
- Created unique index on `(round_id, team_id)` to enable `REFRESH CONCURRENTLY`
- Updated `ResetScores()` to refresh the materialized view when scores are reset

**SQL Created:**
```sql
CREATE MATERIALIZED VIEW IF NOT EXISTS cumulative_scores AS
SELECT DISTINCT 
    round_id, 
    team_id, 
    SUM(CASE WHEN result = '1' THEN points ELSE 0 END) 
        OVER(PARTITION BY team_id ORDER BY round_id) as cumulative_points
FROM service_check_schemas 
ORDER BY team_id, round_id;

CREATE UNIQUE INDEX IF NOT EXISTS idx_cumulative_scores_round_team 
ON cumulative_scores (round_id, team_id);
```

### 2. Refresh Function (`engine/db/rounds.go`)
- Added `RefreshScoresMaterializedView()` function
- Uses `REFRESH MATERIALIZED VIEW CONCURRENTLY` to avoid blocking reads during refresh

### 3. Automatic Refresh Trigger (`engine/engine.go`)
- Modified `processCollectedResults()` to refresh the materialized view after processing each round
- Refresh runs asynchronously in a goroutine to avoid blocking the next round
- Logs errors if refresh fails without disrupting the engine

### 4. Optimized Query (`engine/db/servicechecks.go`)
- Updated `GetServiceCheckSumByRound()` to query from `cumulative_scores` view
- Replaced expensive window function with simple SELECT from materialized view
- SLA penalty calculation remains unchanged (still queried from `sla_schemas`)

## Benefits

1. **Performance**: Graph API (`GetScoreStatus`) now queries pre-computed results instead of running window functions
2. **Scalability**: Query time is constant regardless of number of rounds
3. **Non-blocking**: CONCURRENT refresh allows reads during updates
4. **Automatic**: View refreshes after each round without manual intervention

## Technical Notes

### Unique Index Requirement
The unique index on `(round_id, team_id)` is required for CONCURRENT refresh. Without it, PostgreSQL would lock the view during refresh, blocking all reads.

### SLA Penalties
SLA penalties are still calculated separately and applied to the cumulative scores in the application layer. This maintains the existing business logic where SLA penalties affect all subsequent rounds.

### Initial State
The materialized view is created empty during database initialization. It will be populated as rounds are processed. If the database already contains round data, a manual `REFRESH MATERIALIZED VIEW cumulative_scores` should be run once.

### Error Handling
- View creation errors are fatal (application won't start)
- Refresh errors are logged but don't stop the engine
- This ensures the system remains operational even if materialized view refresh fails

## Future Considerations

1. **SLA Penalties View**: Currently SLA penalties are queried separately. Could be combined into the materialized view for further optimization.

2. **Service Status**: `GetServiceStatus` only queries the last round and is already fast. No materialization needed.

3. **Manual Refresh**: Consider adding an admin API endpoint to manually trigger refresh if needed.

4. **Monitoring**: Add metrics to track refresh time and success rate.
