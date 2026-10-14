import React, { useContext } from 'react';
import { Navigate } from 'react-router-dom';
import { UserContext } from '../context/User';
import { isAdmin } from '../helpers';
import Loading from './Loading';

const AdminRoute = ({ children }) => {
  const [userState] = useContext(UserContext);
  if (!userState.sessionReady) return <Loading />;
  if (!userState.user) return <Navigate to='/login' replace />;
  if (!isAdmin()) return <Navigate to='/' replace />;
  return children;
};

export default AdminRoute;
