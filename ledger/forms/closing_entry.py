from django.forms import DateInput, ValidationError, ModelForm, Textarea
from django.utils.translation import gettext_lazy as _

from ledger.io.io_core import get_localdate
from ledger.models.closing_entry import ClosingEntryModel
from ledger.settings import LEDGER_FORM_INPUT_CLASSES


class ClosingEntryCreateForm(ModelForm):

    def clean_closing_date(self):
        closing_date = self.cleaned_data['closing_date']
        if closing_date > get_localdate():
            raise ValidationError(
                message=_('Cannot create a closing entry with a future date.'), code='invalid_date'
            )
        return closing_date

    class Meta:
        model = ClosingEntryModel
        fields = [
            'closing_date'
        ]

        widgets = {
            'closing_date': DateInput(attrs={
                'class': LEDGER_FORM_INPUT_CLASSES + ' is-large',
                'placeholder': _('Closing Date (YYYY-MM-DD)...'),
                'id': 'djl-datepicker',
                'type': 'date'
            })
        }
        labels = {
            'closing_date': _('Select a Closing Date')
        }


class ClosingEntryUpdateForm(ModelForm):
    class Meta:
        model = ClosingEntryModel
        fields = [
            'markdown_notes'
        ]

        widgets = {
            'markdown_notes': Textarea(attrs={
                'class': 'textarea'
            }),
        }
        labels = {
            'markdown_notes': _('Closing Entry Notes')
        }
