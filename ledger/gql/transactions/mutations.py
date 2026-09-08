import logging
from django.core.exceptions import ValidationError
import graphene
from core.schema import BaseMutation
from ledger.tasks import process_import_job
from ledger.models import EntityModel, ImportedJobModel, ImportedTransactionModel


logger = logging.getLogger(__name__)


class CreateImportedJob(BaseMutation):
    """
    Register an imported transactions job after a file has been uploaded to S3,
    then enqueue background processing.
    """
    _mutation_module = "ledger"
    _mutation_class = "CreateImportedJob"
    async_mutations = False

    class Input(BaseMutation.Input):
        operation_type = graphene.String(required=True, description="operation type")
        entity_uuid = graphene.UUID(required=True, description="UUID of the entity")
        file_type = graphene.String(required=True, description="File type (e.g., csv, xlsx, xls)")
        file_name = graphene.String(required=True, description="Original file name")
        source = graphene.String(required=True, description="Import source identifier")

    @classmethod
    def async_mutate(cls, user, **data):
        """
        Creates an ImportedJobModel and enqueues the processing task.
        Returns [] on success, or a list of error dicts on failure.
        """
        try:
            # Validate operation_type with DocumentOperationType enum
            try:
                from ledger.gql.transactions.types import DocumentOperationType
                allowed_types = {member.value for member in DocumentOperationType._meta.enum}
            except Exception:
                allowed_types = {
                    "bank_statement", "receipt", "invoice", "transaction_upload",
                    "deposit", "withdrawal", "transfer", "payment"
                }
            if data["operation_type"] not in allowed_types:
                raise ValidationError(
                    f"Operation type '{data['operation_type']}' is not allowed. "
                    f"Allowed types: {', '.join(sorted(allowed_types))}"
                )

            
            # Validate choices for file_type and source against model enums
            valid_file_types = {choice for choice, _ in ImportedJobModel.FileTypeChoices.choices}
            valid_sources = {choice for choice, _ in ImportedJobModel.SourceChoices.choices}
            if data["file_type"] not in valid_file_types:
                raise ValidationError(
                    f"File type '{data['file_type']}' is not allowed. "
                    f"Allowed types: {', '.join(sorted(valid_file_types))}"
                )
            if data["source"] not in valid_sources:
                raise ValidationError(
                    f"Source '{data['source']}' is not allowed. "
                    f"Allowed sources: {', '.join(sorted(valid_sources))}"
                )
            entity = EntityModel.objects.get(uuid=data["entity_uuid"])
            logger.info(
                "Creating ImportedJobModel for entity=%s file_name=%s file_type=%s source=%s operation_type=%s",
                data["entity_uuid"], data["file_name"], data["file_type"], data["source"], data["operation_type"]
            )
            job = ImportedJobModel.objects.create(
                entity=entity,
                source=data["source"],
                file_type=data["file_type"],
                file_name=data["file_name"],
                status=ImportedJobModel.StatusChoices.PENDING,
                operation_type=data["operation_type"],
            )
            try:
                if job.pk:
                    logger.info("ImportedJobModel created uuid=%s; enqueuing process_import_job", job.pk)
                    process_import_job.delay(str(job.pk))
                else:
                    return [{
                        "message": "Failed to create imported job.",
                        "user_message": "Failed to create imported job."
                    }]
            except Exception as enqueue_exc:
                logger.exception("Failed to enqueue process_import_job for %s: %s", job.pk, enqueue_exc)
                return [{
                    "message": str(enqueue_exc),
                    "user_message": "Could not start import processing; please retry."
                }]
            return []
        except EntityModel.DoesNotExist:
            return [{
                "message": "Entity not found.",
                "user_message": "Invalid entity."
            }]
        except Exception as exc:
            logger.exception("CreateImportedJob failed: %s", exc)
            return [{
                "message": str(exc),
                "user_message": "Failed to register import job."
            }]


