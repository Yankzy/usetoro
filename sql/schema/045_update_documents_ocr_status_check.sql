-- +goose Up
-- +goose StatementBegin
ALTER TABLE toro_core.documents 
  DROP CONSTRAINT IF EXISTS documents_ocr_status_check;

UPDATE toro_core.documents 
  SET ocr_status = 'OCR_SUCCESS' 
  WHERE ocr_status = 'PROCESSED';

ALTER TABLE toro_core.documents 
  ADD CONSTRAINT documents_ocr_status_check 
  CHECK (ocr_status IN ('PENDING', 'PROCESSING', 'PROCESSED', 'OCR_SUCCESS', 'EMBEDDINGS_SUCCESS', 'FAILED'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE toro_core.documents 
  DROP CONSTRAINT IF EXISTS documents_ocr_status_check;

ALTER TABLE toro_core.documents 
  ADD CONSTRAINT documents_ocr_status_check 
  CHECK (ocr_status IN ('PENDING', 'PROCESSING', 'PROCESSED', 'FAILED'));
-- +goose StatementEnd

