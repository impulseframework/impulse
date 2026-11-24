SELECT
  sm.SquadronId AS SquadronId,
  sm.UserId AS UserId,
  w.SectorId AS SectorId,
  w.Name AS WingName
FROM SquadronMemberships sm
JOIN Squadrons s ON s.Id = sm.SquadronId
JOIN Wings w ON w.Id = s.WingId
