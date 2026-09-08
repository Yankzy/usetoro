from ledger.tests.base import DjangoLedgerBaseTest
from ledger.models import EntityModel, LedgerModel

class EphemeralEntityTest(DjangoLedgerBaseTest):

    @classmethod
    def setUpTestData(cls):
        # Minimal setup copied/adapted from base.py to avoid broken populate_entity_models
        cls.USERNAME = 'testuser'
        cls.PASSWORD = '@password1234'
        cls.USER_EMAIL = 'testuser@fignode.com'
        
        from django.contrib.auth import get_user_model
        from core.models import LanguageModel
        
        LanguageModel.objects.get_or_create(code='en', name='English')
        
        UserModel = get_user_model()
        
        try:
            cls.user_model = UserModel.objects.get(email=cls.USER_EMAIL)
        except UserModel.DoesNotExist:
            cls.user_model = UserModel.objects.create_user(
                email=cls.USER_EMAIL,
                password=cls.PASSWORD,
            )
            
    def test_ephemeral_entity_deletion(self):
        # Create ephemeral entity
        entity = EntityModel(
            name="Ephemeral Project",
            is_ephemeral=True,
            admin=self.user_model
        )
        entity = EntityModel.add_root(instance=entity)
        
        # Create ledger and post it
        ledger = LedgerModel.objects.create(
            entity=entity,
            name="Test Ledger"
        )
        ledger.post(commit=True)
        
        self.assertTrue(ledger.posted)
        self.assertTrue(entity.is_ephemeral)
        
        # Try to delete Ledger (should work for ephemeral entity)
        # In normal entities, deleting a posted ledger raises ValidationError
        ledger.delete()
        self.assertFalse(LedgerModel.objects.filter(uuid=ledger.uuid).exists())
        
        # Create another ledger and try to delete Entity (should work)
        ledger2 = LedgerModel.objects.create(
            entity=entity,
            name="Test Ledger 2"
        )
        ledger2.post(commit=True)
        
        # Deleting entity cascades to ledger. 
        # If ledger.delete() raises ValidationError, entity.delete() would fail if signals/cascades trigger it?
        # Typically Django's cascade delete doesn't call .delete() on related objects unless using signals or custom logic?
        # But LedgerModel.delete() logic is what we modified.
        # If EntityModel.delete() cascades to LedgerModel, does it call LedgerModel.delete()?
        # Django's standard CASCADE deletion does NOT call model's delete() method depending on how it's invoked.
        # However, we should verify that we can delete the entity.
        
        entity.delete()
        self.assertFalse(EntityModel.objects.filter(uuid=entity.uuid).exists())

    def test_normal_entity_deletion_fails(self):
        # Verify standard behavior is preserved
        entity = EntityModel(
            name="Normal Project",
            is_ephemeral=False,
            admin=self.user_model
        )
        entity = EntityModel.add_root(instance=entity)
        
        ledger = LedgerModel.objects.create(
            entity=entity,
            name="Test Ledger Normal"
        )
        ledger.post(commit=True)
        
        from django.core.exceptions import ValidationError
        
        # Try to delete Ledger directly - should fail
        with self.assertRaises(ValidationError):
            ledger.delete()
            
        # Try to delete Entity - if it cascades and checks, it might fail?
        # Note: EntityModel.delete() doesn't explicitly check sub-objects unless implemented.
        # But LedgerModel might have signals.
        # Let's focus on LedgerModel.delete() failure which we explicitly patched.
