import graphene
from users.models import User
from .types import UserNode
from django.contrib.auth import authenticate
from rest_framework.authtoken.models import Token

class RegisterUser(graphene.Mutation):
    user = graphene.Field(UserNode)

    class Arguments:
        email = graphene.String(required=True)
        password = graphene.String(required=True)
        first_name = graphene.String(required=False)
        last_name = graphene.String(required=False)

    def mutate(self, info, email, password, first_name=None, last_name=None):
        user = User.objects.create_user(
            email=email,
            password=password,
            first_name=first_name or '',
            last_name=last_name or ''
        )
        return RegisterUser(user=user)

class LoginUser(graphene.Mutation):
    user = graphene.Field(UserNode)
    token = graphene.String()

    class Arguments:
        email = graphene.String(required=True)
        password = graphene.String(required=True)

    def mutate(self, info, email, password):
        user = authenticate(username=email, password=password)
        if not user:
            raise Exception("Invalid credentials")
        
        token, _ = Token.objects.get_or_create(user=user)
        return LoginUser(user=user, token=token.key)

class Mutation(graphene.ObjectType):
    register_user = RegisterUser.Field()
    login_user = LoginUser.Field()
