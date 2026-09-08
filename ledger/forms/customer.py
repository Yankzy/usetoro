"""



"""

from django.forms import ModelForm, TextInput, EmailInput, NumberInput
from django.utils.translation import gettext_lazy as _

from ledger.forms.utils import validate_cszc
from ledger.models.customer import CustomerModel
from ledger.settings import LEDGER_FORM_INPUT_CLASSES


class CustomerModelForm(ModelForm):

    def clean(self):
        validate_cszc(self.cleaned_data)

    class Meta:
        model = CustomerModel
        fields = [
            'customer_name',
            'address_1',
            'address_2',
            'city',
            'state',
            'zip_code',
            'country',
            'phone',
            'email',
            'website',
            'sales_tax_rate',
            'active',
            'hidden'
        ]
        help_texts = {
            'sales_tax_rate': _('Example: 3.50% should be entered as 0.035')
        }
        widgets = {
            'customer_name': TextInput(attrs={
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
                'class': LEDGER_FORM_INPUT_CLASSES,
            }),
            'phone': TextInput(attrs={
                'class': LEDGER_FORM_INPUT_CLASSES,
            }),
            'email': EmailInput(attrs={
                'class': LEDGER_FORM_INPUT_CLASSES
            }),
            'website': TextInput(attrs={
                'class': LEDGER_FORM_INPUT_CLASSES
            }),
            'sales_tax_rate': NumberInput(attrs={
                'class': LEDGER_FORM_INPUT_CLASSES,
                'min': 0.000,
                'max': 1.000,
                'step': 0.001
            })
        }
