from django.urls import include, path

urlpatterns = [
    path('entity/', include('ledger.urls.entity')),
]