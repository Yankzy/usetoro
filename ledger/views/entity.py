"""



"""

from datetime import timedelta
from decimal import Decimal
from random import randint

from django.http import HttpResponseRedirect
from django.urls import reverse
from django.utils.translation import gettext_lazy as _
from django.views.generic import ListView, DetailView, UpdateView, CreateView, RedirectView, DeleteView

from ledger.forms.entity import EntityModelUpdateForm, EntityModelCreateForm
from ledger.io.io_core import get_localdate, get_localtime
from ledger.io.io_generator import EntityDataGenerator
from ledger.models import (
    EntityModel,
    ItemTransactionModel,
    TransactionModel
)
from ledger.views.mixins import (
    QuarterlyReportMixIn, YearlyReportMixIn,
    MonthlyReportMixIn, DateReportMixIn, DjangoLedgerSecurityMixIn, EntityUnitMixIn,
    DigestContextMixIn, UnpaidElementsMixIn, BaseDateNavigationUrlMixIn
)
from django.views import View
from django.http import JsonResponse
from django.views.decorators.csrf import csrf_exempt
from django.utils.decorators import method_decorator
from ledger.models.entity import EntityModel
from ledger.forms.entity import EntityModelCreateForm
from django.contrib.auth import get_user_model
import json
from ledger.models.chart_of_accounts import ChartOfAccountModel





class EntityModelModelViewQuerySetMixIn:
    queryset = None

    def get_queryset(self):
        if self.queryset is None:
            self.queryset = EntityModel.objects.for_user(
                user_model=self.request.user).select_related('default_coa')
        return super().get_queryset()


# Entity CRUD Views ----
class EntityModelListView(DjangoLedgerSecurityMixIn, EntityModelModelViewQuerySetMixIn, ListView):
    template_name = 'ledger/entity/entitiy_list.html'
    context_object_name = 'entities'
    PAGE_TITLE = _('My Entities')
    extra_context = {
        'header_title': PAGE_TITLE,
        'page_title': PAGE_TITLE
    }



from django.views import View
from django.http import JsonResponse
from django.views.decorators.csrf import csrf_exempt
from django.utils.decorators import method_decorator
from ledger.forms.entity import EntityModelCreateForm
from ledger.models.entity import EntityModel
from ledger.io.io_generator import EntityDataGenerator
from ledger.io.io_core import get_localtime
from ledger.models.chart_of_accounts import ChartOfAccountModel
from decimal import Decimal
from datetime import timedelta
import json

@method_decorator(csrf_exempt, name='dispatch')
class EntityModelCreateView(View):
    """
    API-style view to create an EntityModel without authentication, using JSON input and returning JSON.
    """
    def post(self, request, *args, **kwargs):
        try:
            data = json.loads(request.body)
        except Exception:
            return JsonResponse({
                "success": False,
                "errors": {"__all__": ["Invalid JSON."]},
                "message": "Validation failed."
            }, status=400)

        form = EntityModelCreateForm(data)
        if form.is_valid():
            cleaned_data = form.cleaned_data
            default_coa = cleaned_data.get('default_coa')
            activate_accounts = cleaned_data.get('activate_all_accounts')
            sample_data = cleaned_data.get('generate_sample_data')

            # Accept admin as an explicit field in JSON (required)
            admin_id = data.get('admin')
            if not admin_id:
                return JsonResponse({
                    "success": False,
                    "errors": {"admin": ["This field is required."]},
                    "message": "Validation failed."
                }, status=400)
            from django.contrib.auth import get_user_model
            User = get_user_model()
            try:
                admin_user = User.objects.get(pk=admin_id)
            except User.DoesNotExist:
                return JsonResponse({
                    "success": False,
                    "errors": {"admin": ["User with this ID does not exist."]},
                    "message": "Validation failed."
                }, status=400)

            entity_model = EntityModel(
                name=cleaned_data['name'],
                slug=EntityModel.generate_slug_from_name(name=cleaned_data['name']),
                address_1=cleaned_data['address_1'],
                address_2=cleaned_data['address_2'],
                city=cleaned_data['city'],
                state=cleaned_data['state'],
                zip_code=cleaned_data['zip_code'],
                country=cleaned_data['country'],
                email=cleaned_data['email'],
                website=cleaned_data['website'],
                phone=cleaned_data['phone'],
                fy_start_month=cleaned_data['fy_start_month'],
                accrual_method=cleaned_data['accrual_method'],
                admin=admin_user
            )
            entity_model = EntityModel.add_root(instance=entity_model)
            default_coa_model = entity_model.create_chart_of_accounts(assign_as_default=True, commit=True)

            if default_coa:
                entity_model.populate_default_coa(
                    activate_accounts=activate_accounts,
                    coa_model=default_coa_model
                )

            if sample_data:
                entity_generator = EntityDataGenerator(
                    entity_model=entity_model,
                    user_model=admin_user,
                    start_dttm=get_localtime() - timedelta(days=30 * 8),
                    capital_contribution=Decimal.from_float(50000),
                    days_forward=30 * 7,
                    tx_quantity=50
                )
                entity_generator.populate_entity()

            return JsonResponse({
                "success": True,
                "uuid": str(entity_model.uuid),
                "name": entity_model.name,
                "slug": entity_model.slug,
                "message": "Entity created successfully."
            }, status=201)
        else:
            return JsonResponse({
                "success": False,
                "errors": form.errors,
                "message": "Validation failed."
            }, status=400)


class EntityModelUpdateView(DjangoLedgerSecurityMixIn, EntityModelModelViewQuerySetMixIn, UpdateView):
    context_object_name = 'entity'
    template_name = 'ledger/entity/entity_update.html'
    form_class = EntityModelUpdateForm
    slug_url_kwarg = 'entity_slug'

    def get_context_data(self, **kwargs):
        context = super().get_context_data(**kwargs)
        context['page_title'] = self.object.name
        context['header_title'] = self.object.name
        return context

    def get_success_url(self):
        return reverse('ledger:entity-list')


class EntityDeleteView(DjangoLedgerSecurityMixIn, EntityModelModelViewQuerySetMixIn, DeleteView):
    slug_url_kwarg = 'entity_slug'
    context_object_name = 'entity'
    template_name = 'ledger/entity/entity_delete.html'

    def get_context_data(self, *, object_list=None, **kwargs):
        context = super().get_context_data(object_list=object_list, **kwargs)
        context['page_title'] = _('Delete Entity ') + self.object.name
        context['header_title'] = context['page_title']
        return context

    def get_success_url(self):
        return reverse('ledger:home')

    def form_valid(self, form):
        entity_model: EntityModel = self.get_object()
        entity_model.default_coa = None
        entity_model.save(update_fields=['default_coa'])

        ItemTransactionModel.objects.for_entity(
            user_model=self.request.user,
            entity_slug=self.kwargs['entity_slug']
        ).delete()

        TransactionModel.objects.for_entity(
            user_model=self.request.user,
            entity_slug=self.kwargs['entity_slug']
        ).delete()

        return super().form_valid(form=form)


# DASHBOARD VIEWS START ----
class EntityModelDetailHandlerView(DjangoLedgerSecurityMixIn,
                                   EntityUnitMixIn,
                                   RedirectView):

    def get_redirect_url(self, *args, **kwargs):
        loc_date = get_localdate()
        unit_slug = self.get_unit_slug()
        if unit_slug:
            return reverse('ledger:unit-dashboard-month',
                           kwargs={
                               'entity_slug': self.kwargs['entity_slug'],
                               'unit_slug': unit_slug,
                               'year': loc_date.year,
                               'month': loc_date.month,
                           })
        return reverse('ledger:entity-dashboard-month',
                       kwargs={
                           'entity_slug': self.kwargs['entity_slug'],
                           'year': loc_date.year,
                           'month': loc_date.month,
                       })


class EntityModelDetailBaseView(DjangoLedgerSecurityMixIn,
                                EntityModelModelViewQuerySetMixIn,
                                BaseDateNavigationUrlMixIn,
                                UnpaidElementsMixIn,
                                EntityUnitMixIn,
                                DigestContextMixIn,
                                YearlyReportMixIn,
                                DetailView):
    context_object_name = 'entity'
    slug_url_kwarg = 'entity_slug'
    template_name = 'ledger/entity/entity_dashboard.html'
    DJL_NO_FROM_DATE_RAISE_404 = False
    DJL_NO_TO_DATE_RAISE_404 = False

    FETCH_UNPAID_BILLS = True
    FETCH_UNPAID_INVOICES = True

    IO_DIGEST_UNBOUNDED = True
    IO_DIGEST_BOUNDED = True

    def get_context_data(self, **kwargs):
        context = super().get_context_data(**kwargs)
        entity_model: EntityModel = self.object
        context['page_title'] = entity_model.name
        context['header_title'] = entity_model.name
        context['header_subtitle'] = _('Dashboard')
        context['header_subtitle_icon'] = 'mdi:monitor-dashboard'

        unit_slug = context.get('unit_slug', self.get_unit_slug())
        KWARGS = dict(entity_slug=self.kwargs['entity_slug'])

        if unit_slug:
            KWARGS['unit_slug'] = unit_slug

        url_pointer = 'entity' if not unit_slug else 'unit'
        context['pnl_chart_id'] = f'djl-entity-pnl-chart-{randint(10000, 99999)}'
        context['pnl_chart_endpoint'] = reverse(f'ledger:{url_pointer}-json-pnl', kwargs=KWARGS)
        context['payables_chart_id'] = f'djl-entity-payables-chart-{randint(10000, 99999)}'
        context['payables_chart_endpoint'] = reverse(f'ledger:{url_pointer}-json-net-payables', kwargs=KWARGS)
        context['receivables_chart_id'] = f'djl-entity-receivables-chart-{randint(10000, 99999)}'
        context['receivables_chart_endpoint'] = reverse(f'ledger:{url_pointer}-json-net-receivables',
                                                        kwargs=KWARGS)

        return context


class FiscalYearEntityModelDashboardView(EntityModelDetailBaseView):
    """
    Entity Fiscal Year Dashboard View.
    """


class QuarterlyEntityDashboardView(FiscalYearEntityModelDashboardView, QuarterlyReportMixIn):
    """
    Entity Quarterly Dashboard View.
    """


class MonthlyEntityDashboardView(FiscalYearEntityModelDashboardView, MonthlyReportMixIn):
    """
    Monthly Entity Dashboard View.
    """


class DateEntityDashboardView(FiscalYearEntityModelDashboardView, DateReportMixIn):
    """
    Date-specific Entity Dashboard View.
    """
