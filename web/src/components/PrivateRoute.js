import { useContext } from 'react';
import { Navigate } from 'react-router-dom';

import { history } from '../helpers';
import { UserContext } from '../context/User';
import Loading from './Loading';


function PrivateRoute({ children }) {
  const [userState] = useContext(UserContext);
  if (!userState.sessionReady) {
    return <Loading />;
  }
  if (!userState.user) {
    return <Navigate to='/login' state={{ from: history.location }} />;
  }
  return children;
}

export { PrivateRoute };
