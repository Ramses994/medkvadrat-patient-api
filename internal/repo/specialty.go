package repo

// specialtySelectSQL is one expression for the doctor's specialty label (SQL Server 2008).
// S24: MEDDEP.FM_DEP_PROF_ID is empty on doctor departments — use SPECIALISATION instead.
// 1) non-archived MEDDEP rows of the line doctor (PL_SUBJ.MEDECINS_ID) joined to
//    non-archived SPECIALISATION;
// 2) if exactly one SPECIALISATION_ID — ISNULL(NULLIF(LTRIM(RTRIM(WEB_NAME)),''), NAME);
// 3) if several or none — empty. Do not use FM_DEP_PROF or PL_SUBJ.MEDDEP_ID.
const specialtySelectSQL = `
CASE
  WHEN (
    SELECT COUNT(DISTINCT md.SPECIALISATION_ID)
    FROM MEDDEP md
    INNER JOIN SPECIALISATION sp ON sp.SPECIALISATION_ID = md.SPECIALISATION_ID
    WHERE md.MEDECINS_ID = ps.MEDECINS_ID
      AND ISNULL(md.ARCHIVE, 0) = 0
      AND ISNULL(sp.ARCHIVE, 0) = 0
      AND md.SPECIALISATION_ID IS NOT NULL
  ) = 1
  THEN (
    SELECT TOP 1 ISNULL(NULLIF(LTRIM(RTRIM(ISNULL(sp.WEB_NAME, ''))), ''), sp.NAME)
    FROM MEDDEP md
    INNER JOIN SPECIALISATION sp ON sp.SPECIALISATION_ID = md.SPECIALISATION_ID
    WHERE md.MEDECINS_ID = ps.MEDECINS_ID
      AND ISNULL(md.ARCHIVE, 0) = 0
      AND ISNULL(sp.ARCHIVE, 0) = 0
      AND md.SPECIALISATION_ID IS NOT NULL
  )
  ELSE ''
END`
