package booking

const (
	MsgNotFound        = "Запись не найдена"
	MsgSlotTaken       = "Это время только что заняли. Выберите другое"
	MsgSlotLocked      = "Регистратура сейчас записывает на это время. Попробуйте через минуту или выберите другое"
	MsgSlotUnavailable = "Это время недоступно для записи. Выберите другое"
	MsgPatientConflict = "У вас уже есть запись на это время"
	MsgRegistry        = "Не удалось записаться. Позвоните в регистратуру: +7 (499) 288-88-14"
	MsgBookUnknown     = "Проверяем запись. Обновите „Мои записи“ через минуту; если записи нет — позвоните в регистратуру"
	MsgInProgress      = "Предыдущая операция ещё проверяется. Обновите „Мои записи“ через минуту или позвоните в регистратуру"
	MsgNotDone         = "Операция не выполнена. Попробуйте ещё раз или позвоните в регистратуру"
	MsgCancelSupport   = "Эту запись можно отменить только через регистратуру: +7 (499) 288-88-14"
	MsgCancelFailed    = "Не удалось отменить. Попробуйте ещё раз или позвоните в регистратуру"
	MsgCancelPartial   = "Отмена не завершена — регистратура проверит запись и свяжется с вами"
	MsgCancelUnknown   = "Проверяем отмену. Если запись останется — позвоните в регистратуру"
	MsgLimitFuture     = "Слишком много будущих записей. Отмените одну или позвоните в регистратуру: +7 (499) 288-88-14"
	MsgDoctorDay       = "В этот день вы уже записаны к этому врачу"
	MsgAlreadyCancel   = "Запись уже отменена"
	MsgManualActive    = "запись отменена, направление активно"
	PhoneHint          = "+7 (499) 288-88-14"
)

func Message(code string) string {
	switch code {
	case "SLOT_TAKEN":
		return MsgSlotTaken
	case "SLOT_LOCKED":
		return MsgSlotLocked
	case "SLOT_UNAVAILABLE":
		return MsgSlotUnavailable
	case "PATIENT_TIME_CONFLICT":
		return MsgPatientConflict
	case "BOOKING_UNKNOWN":
		return MsgBookUnknown
	case "OPERATION_IN_PROGRESS":
		return MsgInProgress
	case "OPERATION_NOT_DONE":
		return MsgNotDone
	case "CANCEL_NOT_SUPPORTED":
		return MsgCancelSupport
	case "CANCEL_FAILED":
		return MsgCancelFailed
	case "CANCEL_PARTIAL", "EXTERNAL_CHANGE":
		return MsgCancelPartial
	case "CANCEL_UNKNOWN":
		return MsgCancelUnknown
	case "LIMIT_FUTURE":
		return MsgLimitFuture
	case "LIMIT_DOCTOR_DAY":
		return MsgDoctorDay
	case "ALREADY_CANCELLED":
		return MsgAlreadyCancel
	case "CANCEL_TOO_LATE":
		return "Отмена доступна не позднее чем за указанное время до приёма"
	case "NOT_FOUND":
		return MsgNotFound
	default:
		return MsgRegistry
	}
}

// StatusFor is the HTTP status of a stored code on replay.
func StatusFor(code string) int {
	switch code {
	case "SLOT_TAKEN", "SLOT_LOCKED", "SLOT_UNAVAILABLE", "PATIENT_TIME_CONFLICT", "ALREADY_CANCELLED", "CANCEL_NOT_SUPPORTED", "OPERATION_IN_PROGRESS", "OPERATION_NOT_DONE":
		return 409
	case "LIMIT_FUTURE", "LIMIT_DOCTOR_DAY", "LIMIT_MIS", "BOOKING_REJECTED", "CANCEL_TOO_LATE", "IDEMPOTENCY_MISMATCH":
		return 422
	case "BOOKING_ERROR":
		return 500
	case "WRITE_UNAVAILABLE", "BOOKING_UNKNOWN", "SLOTS_UNAVAILABLE", "CANCEL_FAILED", "CANCEL_PARTIAL", "CANCEL_UNKNOWN", "EXTERNAL_CHANGE":
		return 503
	case "WRITE_DISABLED", "WRITE_NOT_ALLOWED":
		return 403
	case "NOT_FOUND":
		return 404
	case "WRITE_NOT_CONFIGURED", "SLOT_TOKEN_UNAVAILABLE":
		return 503
	default:
		return 500
	}
}
