import logging
from typing import Any, Dict, List, Optional, Type
from pydantic import BaseModel, create_model, ValidationError
from .models import WebhookSchema

logger = logging.getLogger(__name__)

class SchemaValidationService:
    _model_cache: Dict[str, Type[BaseModel]] = {}

    @classmethod
    def get_model(cls, source: str) -> Optional[Type[BaseModel]]:
        """
        Retrieves or builds the Pydantic model for a given source.
        Uses in-memory caching to avoid DB hits and model regeneration.
        
        NOTE: In production, we'd need cache invalidation (e.g. redis pub/sub) 
        if schemas change at runtime across multiple workers.
        """
        if source in cls._model_cache:
            return cls._model_cache[source]

        try:
            schema_obj = WebhookSchema.objects.get(source=source, is_active=True)
        except WebhookSchema.DoesNotExist:
            return None

        model = cls._build_dynamic_model(source, schema_obj.schema_definition)
        cls._model_cache[source] = model
        return model

    @classmethod
    def validate_payload(cls, source: str, payload: Dict[str, Any]) -> Dict[str, Any]:
        """
        Validates the payload against the dynamic schema.
        Returns the validated data dict if successful.
        Raises ValidationError if invalid.
        """
        model = cls.get_model(source)
        if not model:
            logger.warning(f"No active schema found for source '{source}'. Skipping validation.")
            return payload # Fail open or closed? For now, fail open (return raw)
        
        validated_instance = model(**payload)
        return validated_instance.model_dump()

    @classmethod
    def _build_dynamic_model(cls, model_name: str, schema_def: Dict[str, Any]) -> Type[BaseModel]:
        """
        Constructs a Pydantic model from a dictionary definition.
        """
        fields = {}
        
        type_mapping = {
            "str": str,
            "int": int,
            "float": float,
            "bool": bool,
            "list": List,
            "dict": Dict,
        }

        for field_name, rules in schema_def.items():
            field_type_str = rules.get("type", "str")
            field_type = type_mapping.get(field_type_str, str)
            
            is_required = rules.get("required", True)
            default_value = rules.get("default", ...)
            
            if not is_required and default_value == ...:
                default_value = None
                field_type = Optional[field_type]
            
            fields[field_name] = (field_type, default_value)
        
        return create_model(model_name, **fields)
