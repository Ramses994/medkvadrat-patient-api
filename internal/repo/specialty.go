package repo

// specialtySelectSQL is one expression for the doctor's specialty label (SQL Server 2008).
// 1) department of the schedule line (PL_SUBJ.MEDDEP_ID → MEDDEP → FM_DEP_PROF.LABEL);
// 2) otherwise the only non-archived MEDDEP profile of the line's doctor;
// 3) otherwise empty. Source may be adjusted after owner check S24.
const specialtySelectSQL = `
ISNULL((
  SELECT TOP 1 fp.LABEL
  FROM MEDDEP md
  INNER JOIN FM_DEP_PROF fp ON fp.FM_DEP_PROF_ID = md.FM_DEP_PROF_ID
  WHERE md.MEDDEP_ID = ps.MEDDEP_ID
    AND md.FM_DEP_PROF_ID IS NOT NULL
), ISNULL((
  SELECT CASE WHEN COUNT(DISTINCT md2.FM_DEP_PROF_ID) = 1 THEN MIN(fp2.LABEL) ELSE '' END
  FROM MEDDEP md2
  INNER JOIN FM_DEP_PROF fp2 ON fp2.FM_DEP_PROF_ID = md2.FM_DEP_PROF_ID
  WHERE md2.MEDECINS_ID = ps.MEDECINS_ID
    AND ISNULL(md2.ARCHIVE, 0) = 0
    AND md2.FM_DEP_PROF_ID IS NOT NULL
), ''))`
