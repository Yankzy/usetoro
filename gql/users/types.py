import graphene
from graphene_django import DjangoObjectType
from users.models import User

class UserNode(DjangoObjectType):
    class Meta:
        model = User
        fields = ("id", "email", "first_name", "last_name", "date_joined", "last_login")
        # Add other fields as necessary based on the User model
