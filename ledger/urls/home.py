from django.urls import path

from ledger import views

urlpatterns = [
    path('my-dashboard/', views.DashboardView.as_view(), name='home'),
]
