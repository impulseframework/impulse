SELECT
  m.Id AS Id,
  m.SectorId AS SectorId,
  m.Title AS Title,
  m.Title_Search AS Title_Search,
  m.ClientId AS ClientId,
  m.AssignedSquadronId AS SquadronId,
  m.KindId AS KindId,
  m.StatusId AS StatusId,
  m.Deadline AS Deadline,
  TIMESTAMP_DIFF(m.Deadline, CURRENT_TIMESTAMP(), DAY) AS DaysLeft
FROM Missions m
