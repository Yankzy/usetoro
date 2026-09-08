"""



"""

from django.urls import reverse
from django.utils.translation import gettext as _
from django.views.generic import RedirectView, ListView

from ledger.models.entity import EntityModel
from ledger.views.mixins import DjangoLedgerSecurityMixIn


class RootUrlView(RedirectView):

    def get_redirect_url(self, *args, **kwargs):
        if not self.request.user.is_authenticated:
            return reverse('ledger:login')
        return reverse('ledger:home')


class DashboardView(DjangoLedgerSecurityMixIn, ListView):
    template_name = 'ledger/entity/home.html'
    PAGE_TITLE = _('My Dashboard')
    context_object_name = 'entities'
    extra_context = {
        'page_title': PAGE_TITLE,
        'header_title': PAGE_TITLE,
    }

    def get_context_data(self, **kwargs):
        context = super().get_context_data(**kwargs)
        context['header_subtitle'] = self.request.user.get_full_name()
        context['header_subtitle_icon'] = 'ei:user'
        return context

    def get_queryset(self):
        return EntityModel.objects.for_user(
            user_model=self.request.user
        ).order_by('-created')
