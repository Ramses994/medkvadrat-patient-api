package repo

import "database/sql"

type Repos struct {
	Patient  *PatientRepo
	Doctor   *DoctorRepo
	Planning *PlanningRepo
	Motconsu *MotconsuRepo
	Health   *HealthRepo
	Catalog  *CatalogRepo
}

func New(db *sql.DB) Repos {
	return Repos{
		Patient:  &PatientRepo{db: db},
		Doctor:   &DoctorRepo{db: db},
		Planning: &PlanningRepo{db: db},
		Motconsu: &MotconsuRepo{db: db},
		Health:   &HealthRepo{db: db},
		Catalog:  &CatalogRepo{db: db},
	}
}
