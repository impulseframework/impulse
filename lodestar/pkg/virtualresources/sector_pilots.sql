WITH flown AS (
  SELECT
    sm.UserId AS UserId,
    w.SectorId AS SectorId,
    COUNT(*) AS Squadrons
  FROM SquadronMemberships sm
  JOIN Squadrons s ON s.Id = sm.SquadronId
  JOIN Wings w ON w.Id = s.WingId
  GROUP BY sm.UserId, w.SectorId
)
SELECT
  p.Id AS PilotId,
  f.SectorId AS SectorId,
  p.DisplayName AS DisplayName,
  f.Squadrons AS Squadrons,
  p.Clearance AS Clearance,
  EXISTS (
    SELECT 1
    FROM flown o
    WHERE o.UserId = f.UserId AND o.SectorId != f.SectorId
  ) AS FliesElsewhere
FROM flown f
JOIN Pilots p ON p.UserId = f.UserId
