"""
Direct SQL repository for loading source perception documents from toro_core.documents.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
import json
from typing import Any
from uuid import UUID

from django.db import connection


@dataclass(frozen=True)
class DocumentSourceRecord:
    id: UUID
    session_id: str
    document_type: str
    file_name: str
    mime_type: str
    s3_url: str
    sha256: str | None
    ocr_status: str
    raw_ocr_json: dict[str, Any]
    metadata: dict[str, Any]
    created_at: datetime


class DocumentSourceRepository:
    """
    Direct SQL repository for loading source perception documents from toro_core.documents.
    """

    @staticmethod
    def get_document(document_id: UUID | str) -> DocumentSourceRecord | None:
        doc_uuid = UUID(str(document_id))
        query = """
            SELECT 
                id, 
                session_id, 
                document_type, 
                file_name, 
                mime_type, 
                s3_url, 
                sha256, 
                ocr_status, 
                raw_ocr_json, 
                metadata, 
                created_at 
            FROM toro_core.documents 
            WHERE id = %s
        """
        with connection.cursor() as cursor:
            cursor.execute(query, [str(doc_uuid)])
            row = cursor.fetchone()
            if not row:
                return None

            raw_ocr = row[8]
            if isinstance(raw_ocr, str):
                try:
                    raw_ocr = json.loads(raw_ocr)
                except Exception:
                    raw_ocr = {}
            elif not isinstance(raw_ocr, dict):
                raw_ocr = {}

            meta = row[9]
            if isinstance(meta, str):
                try:
                    meta = json.loads(meta)
                except Exception:
                    meta = {}
            elif not isinstance(meta, dict):
                meta = {}

            return DocumentSourceRecord(
                id=row[0] if isinstance(row[0], UUID) else UUID(str(row[0])),
                session_id=str(row[1] or ""),
                document_type=str(row[2] or ""),
                file_name=str(row[3] or ""),
                mime_type=str(row[4] or ""),
                s3_url=str(row[5] or ""),
                sha256=str(row[6]) if row[6] is not None else None,
                ocr_status=str(row[7] or ""),
                raw_ocr_json=raw_ocr,
                metadata=meta,
                created_at=row[10],
            )
