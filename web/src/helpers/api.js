import { showError } from './utils';
import { expireSessionOnUnauthorized } from './session';
import axios from 'axios';

export const API = axios.create({
  baseURL: process.env.REACT_APP_SERVER ? process.env.REACT_APP_SERVER : '',
  withCredentials: true,
});

API.interceptors.response.use(
  (response) => response,
  (error) => {
    expireSessionOnUnauthorized(error);
    if (!error.config?.suppressErrorToast) {
      showError(error);
    }
    return Promise.reject(error);
  }
);
