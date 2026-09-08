from ledger.models.entity import EntityModel
from ledger.models.ledger import LedgerModel
from ledger.models.unit import EntityUnitModel
from ledger.io.io_generator import EntityDataGenerator
from ledger.io.io_core import get_localtime
from ledger.io.utils.default_coas import categories
from decimal import Decimal
from datetime import timedelta
from django.db import transaction
from ledger.models.accounts import AccountModel

def create_entity_utility(**kwargs):
    with transaction.atomic():
        user = kwargs['user']
        fields = {f.name for f in EntityModel._meta.get_fields()}
        valid_fields = {k: v for k, v in kwargs.items() if k in fields and v is not None}
        valid_fields['admin'] = user
        entity = EntityModel(**valid_fields)
        entity = EntityModel.add_root(instance=entity)
        if picture:= kwargs.get('picture'):
            entity.picture = picture
            entity.save(update_fields=['picture'])
        default_coa_model = entity.create_chart_of_accounts(coa_name="general_coa", assign_as_default=True, commit=True)
        if kwargs.get('activate_all_accounts'):
            entity.populate_default_coa(activate_accounts=True, coa_model=default_coa_model)
        LedgerModel.objects.get_or_create(
            name="general_ledger",
            entity=entity,
        )
        # EntityUnitModel.objects.get_or_create(
        #     name="general_unit",
        #     entity=entity,
        #     active=True,
        #     hidden=False,
        #     depth=1,
        # )
        unit = EntityUnitModel.objects.filter(name="general_unit", entity=entity).first()
        if not unit:
            unit = EntityUnitModel.add_root(
                name="general_unit",
                entity=entity,
                active=True,
                hidden=False,
                depth=0,
            )
        for _, cat_data in categories.items():
            for acc in cat_data["accounts"]:
                
                account_model = AccountModel(
                    name=acc["name"],
                    role=acc["role"],
                    balance_type=acc["balance_type"],
                    active=True,
                    coa_model=default_coa_model,
                )

                account_model.clean()  # This will generate code if not set
                default_coa_model.insert_account(account_model=account_model)
                account_model.role_default = False
                account_model.locked = False
                account_model.save(update_fields=['role_default', 'locked'])
        if kwargs.get('generate_sample_data'):
            entity_generator = EntityDataGenerator(
                entity_model=entity,
                user_model=user,
                start_dttm=get_localtime() - timedelta(days=30 * 8),
                capital_contribution=Decimal.from_float(50000),
                days_forward=30 * 7,
                tx_quantity=50
            )
            entity_generator.populate_entity()
        return entity