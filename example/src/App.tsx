import React  from 'react';
import { useFlags, useFlagsmith } from 'flagsmith/react';
import {FlagsmithTypes} from "../flagsmith";

function App() {
    const {json_flag, new_flag} = useFlags<FlagsmithTypes>(['json_flag']); // only causes re-render if specified flag values / traits change
    return (
        <div className='App'>
          Address 1: {json_flag?.value?.address?.line_1}
          Address 2: {json_flag?.value?.address?.line_2}
        </div>
    );
}

export default App;
