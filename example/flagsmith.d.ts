export interface FlagsmithTypes {
    json_flag:   JsonFlag;
    number_flag: number;
    string_flag: string;
}

export interface JsonFlag {
    first_name: string;
    last_name:  string;
    address:    Address;
}

export interface Address {
    line_1: string;
    line_2: string;
}
