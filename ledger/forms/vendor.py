"""



"""

from django.forms import ModelForm, TextInput, EmailInput

from ledger.forms.utils import validate_cszc
from ledger.models.vendor import VendorModel
from ledger.settings import LEDGER_FORM_INPUT_CLASSES


class VendorModelForm(ModelForm):

    def clean(self):
        validate_cszc(self.cleaned_data)

    class Meta:
        model = VendorModel
        fields = [
            'vendor_name',
            'address_1',
            'address_2',
            'city',
            'state',
            'zip_code',
            'country',
            'phone',
            'email',
            'website',
            'tax_id_number',
            'hidden',
            'active'
        ]
        widgets = {
            'vendor_name': TextInput(attrs={
                'class': LEDGER_FORM_INPUT_CLASSES
            }),
            'address_1': TextInput(attrs={
                'class': LEDGER_FORM_INPUT_CLASSES
            }),
            'address_2': TextInput(attrs={
                'class': LEDGER_FORM_INPUT_CLASSES
            }),
            'city': TextInput(attrs={
                'class': LEDGER_FORM_INPUT_CLASSES
            }),
            'state': TextInput(attrs={
                'class': LEDGER_FORM_INPUT_CLASSES
            }),
            'zip_code': TextInput(attrs={
                'class': LEDGER_FORM_INPUT_CLASSES
            }),
            'country': TextInput(attrs={
                'class': LEDGER_FORM_INPUT_CLASSES
            }),
            'phone': TextInput(attrs={
                'class': LEDGER_FORM_INPUT_CLASSES
            }),
            'email': EmailInput(attrs={
                'class': LEDGER_FORM_INPUT_CLASSES
            }),
            'website': TextInput(attrs={
                'class': LEDGER_FORM_INPUT_CLASSES
            }),
            'tax_id_number': TextInput(attrs={
                'class': LEDGER_FORM_INPUT_CLASSES
            }),
        }
