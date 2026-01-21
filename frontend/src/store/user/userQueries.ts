import { gql } from 'graphql-request';

export const GET_USERS = gql`
  query GetUsers {
    users {
      id
      email
      firstName
      lastName
      dateJoined
      lastLogin
    }
  }
`;

export const REGISTER_USER = gql`
  mutation RegisterUser($email: String!, $password: String!, $firstName: String, $lastName: String) {
    registerUser(email: $email, password: $password, firstName: $firstName, lastName: $lastName) {
      user {
        id
        email
        firstName
        lastName
      }
    }
  }
`;

export const LOGIN_USER = gql`
  mutation LoginUser($email: String!, $password: String!) {
    loginUser(email: $email, password: $password) {
      token
      user {
        id
        email
        firstName
        lastName
      }
    }
  }
`;
