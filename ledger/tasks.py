import logging
from celery import shared_task
from django.contrib.auth import get_user_model
from ledger.models import EntityModel, AccountModel
from ledger.bookkeeping_agents.embeddings import embeddings_manager
from django.db import transaction
import io
import pandas as pd
import json
from urllib.request import urlopen
from ledger.models.transactions import ImportedJobModel, ImportedTransactionModel
from core.registry import ServiceRegistryError, get_service


UserModel = get_user_model()

logger = logging.getLogger(__name__)


# @shared_task(bind=True, ignore_result=True)
def daily_post_entity_journal_entries() -> dict:
    """
    Daily task that iterates all EntityModel instances and posts each entity's
    ledger journal entries (unposted JEs). For each ledger it calls
    `ledger.post_journal_entries(commit=True)` which marks JEs as posted
    and persists them via bulk_update for efficiency.
    Returns a small summary dict.
    """
    summary = {"entities": 0, "ledgers": 0, "jes_posted": 0, "errors": 0}

    qs = EntityModel.objects.all().iterator()
    for entity in qs:
        summary["entities"] += 1
        try:
            # get all ledgers for the entity
            ledgers = entity.get_ledgers()
            for ledger in ledgers:
                summary["ledgers"] += 1
                try:
                    # perform ledger-level JE posting inside a transaction for safety
                    with transaction.atomic():
                        je_qs = ledger.post_journal_entries(commit=True)
                    # je_qs may be a QuerySet: count how many were touched
                    try:
                        count = je_qs.count()
                    except Exception:
                        # fallback if not a queryset
                        count = len(list(je_qs))
                    summary["jes_posted"] += count
                    logger.info(
                        "Entity %s: posted %d JEs on ledger %s",
                        getattr(entity, "slug", entity.pk),
                        count,
                        getattr(ledger, "uuid", ledger.pk),
                    )
                except Exception as e:
                    summary["errors"] += 1
                    logger.exception(
                        "Error posting JEs for ledger %s (entity=%s): %s",
                        getattr(ledger, "uuid", ledger.pk),
                        getattr(entity, "slug", entity.pk),
                        e,
                    )
        except Exception as e:
            summary["errors"] += 1
            logger.exception("Error processing entity %s: %s", getattr(entity, "slug", entity.pk), e)

    return summary
 
@shared_task(bind=True, ignore_result=True)
def insert_account_embedding_task(self, account_model_id: str):
    """
    Async task to generate embeddings.
    Fetches the object by ID to be safe.    
    """
    try:
        account_model = AccountModel.objects.get(pk=account_model_id)
        if not account_model:
            logger.warning(f"AccountModel {account_model_id} not found for embedding.")
            return False
        result = embeddings_manager.insert_account_embedding(account_model)
        if result == "insert successful":
            return True
        else:
            logger.error(f"Error inserting account embedding: {result}")
            return False
    except Exception as e:
        logger.exception(f"Error inserting account embedding: {e}")
        return False  




@shared_task(bind=True, max_retries=3)
def process_import_job(self, job_id: str) -> None:
    """
    Async task to process an imported job.
    """
    logger.info(f"Processing import job in task: {job_id}")
    # Load the specific job by id if it's pending, then set processing status
    job = ImportedJobModel.objects.filter(
        pk=job_id,
        status=ImportedJobModel.StatusChoices.PENDING
    ).first()
    if not job:
        logger.warning(f"ImportedJobModel {job_id} not found for processing.")
        return False
    job.status = ImportedJobModel.StatusChoices.PROCESSING
    job.save(update_fields=["status", "updated_at"])
    try:
        # get presigned url to get the signed url with file name and content type, and expiration
        try:
            s3_service = get_service('trading.S3Service')
            presigned_url = s3_service.get_url(job.file_name, job.file_type, 600) # 10 minutes
        except ServiceRegistryError as e:
            logger.exception(f"Error getting presigned url: {e}")
            return False
        # Fetch file bytes from signed URL
        with urlopen(presigned_url) as resp:
            file_bytes = resp.read()

        # Parse dataframe based on file type
        df = _load_df(file_bytes, job.file_type)
        # Replace NaN with None for JSON compatibility
        df = df.where(pd.notnull(df), None)

        logger.info(f"DataFrame: {df}")
        job.status = ImportedJobModel.StatusChoices.COMPLETED
        job.save(update_fields=["status", "updated_at"])
    except Exception as exc:
        logger.exception("Failed processing import job %s: %s", job.pk, exc)
        job.status = ImportedJobModel.StatusChoices.FAILED
        job.save(update_fields=["status", "updated_at"])


def _load_df(file_bytes: bytes, file_type: str) -> pd.DataFrame:
    buffer = io.BytesIO(file_bytes)
    if file_type == "csv":
        return pd.read_csv(buffer)
    if file_type in ("xlsx", "xls"):
        return pd.read_excel(buffer, engine="openpyxl")
    raise ValueError(f"unsupported import type: {file_type}")
