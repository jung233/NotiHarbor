import React, { useContext } from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { UserContext } from '../context/User';
import Loading from './Loading';

const AdminRoute = ({ children }) => {
  const [userState] = useContext(UserContext);
  const location = useLocation();
  if (!userState.sessionReady) return <Loading />;
  if (!userState.user) {
    return <Navigate to='/login' replace state={{ from: location }} />;
  }
  if (!(Number(userState.user.role) >= 10)) {
    return <Navigate to='/' replace />;
  }
  return children;
};

export default AdminRoute;
