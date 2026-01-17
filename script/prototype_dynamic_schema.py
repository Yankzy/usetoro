import json
from typing import Any, Dict, List, Optional, Type
from pydantic import BaseModel, create_model, Field, ValidationError

def create_dynamic_model(model_name: str, schema_def: Dict[str, Any]) -> Type[BaseModel]:
    """
    Dynamically creates a Pydantic model from a dictionary definition.
    
    schema_def structure:
    {
        "field_name": {
            "type": "str" | "int" | "float" | "bool" | "list" | "dict",
            "required": boolean,
            "default": value (optional)
        }
    }
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

def main():
    # 1. Define a schema configuration (this would eventually come from DB or Config)
    stripe_charge_schema = {
        "id": {"type": "str", "required": True},
        "amount": {"type": "int", "required": True},
        "currency": {"type": "str", "required": True},
        "description": {"type": "str", "required": False},
        "paid": {"type": "bool", "required": True},
        "status": {"type": "str", "required": True}
    }

    # 2. Generate the Pydantic model at runtime
    DynamicStripeCharge = create_dynamic_model("DynamicStripeCharge", stripe_charge_schema)
    print(f"Generated Model: {DynamicStripeCharge}")
    print(f"Model Fields: {DynamicStripeCharge.__annotations__}\n")

    # 3. Simulate a Valid Stripe Payload
    valid_payload = {
        "id": "ch_1J2k3L4m5N6o7p8q9r0s",
        "amount": 2000,
        "currency": "usd",
        "description": "Subscription for Premium Plan",
        "paid": True,
        "status": "succeeded"
    }

    print("--- Testing Valid Payload ---")
    try:
        validated_data = DynamicStripeCharge(**valid_payload)
        print("✅ Validation Successful!")
        print(f"Parsed Data: {validated_data.model_dump_json(indent=2)}\n")
    except ValidationError as e:
        print("❌ Validation Failed")
        print(e.json())

    # 4. Simulate an Invalid Payload (Missing required field 'amount', wrong type for 'paid')
    invalid_payload = {
        "id": "ch_bad_payload",
        # "amount": 2000,  <-- MISSING REQUIRED FIELD
        "currency": "usd",
        "paid": "not_a_boolean", # <-- WRONG TYPE
        "status": "failed"
    }

    print("--- Testing Invalid Payload ---")
    try:
        DynamicStripeCharge(**invalid_payload)
        print("✅ Validation Successful (Unexpected!)")
    except ValidationError as e:
        print("✅ Validation correctly rejected invalid data:")
        print(e.json(indent=2))

if __name__ == "__main__":
    main()
