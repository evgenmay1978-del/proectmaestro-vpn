from aiogram.fsm.state import State, StatesGroup

class ClientStates(StatesGroup):
    main = State()
    choosing_tariff = State()
    waiting_payment = State()
    waiting_photo = State()

class AdminStates(StatesGroup):
    broadcast_text = State()
    edit_tariff_price = State()
    add_tariff_days = State()
    add_tariff_price = State()
    client_message = State()
    client_search = State()
    client_days = State()
    client_expiry = State()
