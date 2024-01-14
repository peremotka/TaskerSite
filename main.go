package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"log"
	"net/http"
	"net/mail"
	"net/smtp"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Data struct {
	T        time.Time // время хранения кода
	Code     string
	Password string
}

type User struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Tasks    []Task `json:"tasks"` // Список задач, связанных с пользователем
}

type Task struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Deadline    time.Time `json:"deadline"`
	Complete    bool      `json:"complete"`
}

var (
	sessions = make(map[string]Data) // мапа сессий, где ключ - email пользователя
)

func GetTask(res http.ResponseWriter, req *http.Request) {
	email := req.URL.Query().Get("email")
	taskID := req.URL.Query().Get("task_id")

	user, err := getUserFromBd(email)
	if err != nil {
		http.Error(res, "User not found", http.StatusNotFound)
		log.Println("Пользователь не найден")
		return
	}

	var task Task // пустая переменная типа Task, в которой мы разместим указанную задачу конкретного пользователя

	for _, oneTask := range user.Tasks { // цикл поиска нужной задачи в user.Tasks по taskID из HTTP запроса
		if oneTask.ID == taskID {
			task = oneTask
			break
		}
	}

	// Проверяем, найдена ли задача
	if task.ID == "" {
		http.Error(res, "Task not found", http.StatusNotFound)
		log.Println("Задача не найдена")
		return
	}

	// Преобразуем задачу в JSON
	jsonData, err := json.Marshal(task)
	if err != nil {
		http.Error(res, "Error encoding JSON", http.StatusInternalServerError)
		log.Println("Ошибка кодирования JSON")
		return
	}

	//fmt.Println(string(jsonData))
	res.Write(jsonData)

}

func GetTasks(res http.ResponseWriter, req *http.Request) {
	email := req.URL.Query().Get("email")

	user, err := getUserFromBd(email)
	if err != nil {
		http.Error(res, "User not found", http.StatusNotFound)
		log.Println("Пользователь не найден")
		return
	}

	tasksJSON, err := json.Marshal(user.Tasks)
	if err != nil {
		http.Error(res, "Error encoding tasks to JSON", http.StatusInternalServerError)
		log.Println("Ошибка преобразования задач в JSON")
		return
	}

	// Отправка JSON в ответ HTTP
	res.Header().Set("Content-Type", "application/json")
	res.Write(tasksJSON)

}

// функция для создания задачи и добавления ее в БД
func createTask(res http.ResponseWriter, req *http.Request) {
	// Получаем идентификатор пользователя (в виде его email) и данные задачи из параметров запроса
	email := req.URL.Query().Get("email")
	taskTitle := req.URL.Query().Get("title")
	taskDescription := req.URL.Query().Get("description")

	deadline := req.URL.Query().Get("deadline")

	taskDeadline, err := time.Parse(time.RFC3339, deadline) //  принимает значение параметра и преобразовывает его в формат времени RFC3339.
	if err != nil {
		http.Error(res, "Invalid deadline format", http.StatusBadRequest)
		return
	}

	// Проверка наличия пользователя с указанным идентификатором в базе данных
	user, err := getUserFromBd(email)
	if err != nil {
		http.Error(res, "User not found", http.StatusNotFound)
		log.Println("Пользователь не найден")
		return
	}

	taskID := uuid.New().String() // генерация уникального айди для конкретной задачи

	// Создание задачи
	task := Task{
		ID:          taskID,
		Title:       taskTitle,
		Description: taskDescription,
		Deadline:    taskDeadline,
		Complete:    false,
	}

	// Добавление задачи в список задач пользователя
	user.Tasks = append(user.Tasks, task)

	// Обновление пользователя в базе данных
	output, err := updateTasksInBd(email, user)
	if err != nil {
		http.Error(res, "Failed to add task to database", http.StatusInternalServerError)
		return
	}
	fmt.Fprint(res, output)

	// Ответ с идентификатором добавленной задачи
	response := map[string]string{"taskID": taskID}
	json.NewEncoder(res).Encode(response)
}

// функция для обновления задачи через HTTP запрос
func updateTask(res http.ResponseWriter, req *http.Request) {
	email := req.URL.Query().Get("email")

	var updatedTask Task
	err := json.NewDecoder(req.Body).Decode(&updatedTask)
	if err != nil {
		http.Error(res, "Invalid request body", http.StatusBadRequest)
		log.Println("Неверный формат запроса")
		return
	}

	user, err := getUserFromBd(email)
	if err != nil {
		http.Error(res, "User not found", http.StatusNotFound)
		log.Println("Пользователь не найден")
		return
	}

	// цикл по задачам пользователя, чтобы обновить данные в нужной задаче
	for i, task := range user.Tasks { // task - копия элемента user.Tasks (каждого), так как объявлена внутри цикла?
		if task.ID == updatedTask.ID {
			user.Tasks[i] = updatedTask
			break
		}
	}

	output, err := updateTasksInBd(email, user)
	if err != nil {
		http.Error(res, "Failed to update tasks", http.StatusInternalServerError)
		log.Println("Не удалось обновить задачи в базе данных")
		return
	}

	fmt.Fprint(res, output)

}

// функция для обновления задачи в БД
func updateTasksInBd(email string, user User) (string, error) {
	// Параметры подключения к MongoDB
	clientOptions := options.Client().ApplyURI("mongodb+srv://peremotka:oiS5dwR7A8@taskerusers.abxenef.mongodb.net/?retryWrites=true&w=majority")

	// Установка соединения с MongoDB
	client, err := mongo.Connect(context.Background(), clientOptions)
	if err != nil {
		return "", err
	}

	// Проверка соединения с MongoDB
	err = client.Ping(context.Background(), nil)
	if err != nil {
		return "", err
	}

	// Получение коллекции "users"
	usersCollection := client.Database("taskerUsers").Collection("Users")

	// Обновление пользователя в коллекции "users"
	_, err = usersCollection.UpdateOne(context.Background(), bson.M{"email": email}, bson.M{"$set": user})
	if err != nil {
		return "", err
	}
	return "Пользователь успешно обновлен", nil

}

// функция для удаления задачи в БД
func deleteTask(res http.ResponseWriter, req *http.Request) {
	email := req.URL.Query().Get("email")

	taskID := req.URL.Query().Get("task_id")

	//var user User

	// Параметры подключения к MongoDB
	clientOptions := options.Client().ApplyURI("mongodb+srv://peremotka:oiS5dwR7A8@taskerusers.abxenef.mongodb.net/?retryWrites=true&w=majority")

	// Установка соединения с MongoDB
	client, err := mongo.Connect(context.Background(), clientOptions)
	if err != nil {
		fmt.Fprint(res, "Не удалось подключиться к MongoDB", err)
		return
	}

	// Проверка соединения с MongoDB
	err = client.Ping(context.Background(), nil)
	if err != nil {
		http.Error(res, err.Error(), http.StatusBadRequest)
		return
	}

	// Получение коллекции "users"
	usersCollection := client.Database("taskerUsers").Collection("Users")

	filter := bson.D{
		{"email", email},
		{"tasks", bson.M{"$elemMatch": bson.M{"id": taskID}}},
	}

	// Удаляем указанную задачу пользователя из коллекции c помощью DeleteMany (несколько фильтров)
	_, err = usersCollection.UpdateOne(context.Background(), filter, bson.M{"$pull": bson.M{"tasks": bson.M{"id": taskID}}})
	if err != nil {
		fmt.Fprint(res, "Не удалось удалить задачу пользователя", err)
		return
	}

	fmt.Fprint(res, "Задача пользователя успешно удалена")
	//res.Write([]byte("Задача пользователя успешно удалена"))
}

func main() {
	go deadlineTimer() // запуск функции-таймера в фоновом режиме

	router := mux.NewRouter()

	router.HandleFunc("/tasks", GetTasks).Methods("GET")           // для вывода всех задач пользователя
	router.HandleFunc("/task", GetTask).Methods("GET")             // для вывода конкретной задачи
	router.HandleFunc("/createTask", createTask).Methods("GET")    // для создания задачи
	router.HandleFunc("/updateTask", updateTask).Methods("POST")   // для изменения задачи
	router.HandleFunc("/deleteTask", deleteTask).Methods("GET")    // для удаления задачи
	router.HandleFunc("/register", registerHandler).Methods("GET") // регистрация пользователя
	router.HandleFunc("/regFinish", regFinish).Methods("GET")
	router.HandleFunc("/login", login).Methods("GET")                   // авторизация пользователя
	router.HandleFunc("/delUser", deleteUser).Methods("GET")            // удаление пользователя
	router.HandleFunc("/changePassword", changePassword).Methods("GET") // для изменения пароля пользователя

	log.Fatal(http.ListenAndServe(":8080", router))

}

// функция для регистрации пользователя
func registerHandler(w http.ResponseWriter, r *http.Request) {
	//w.Header().Set("Content-Type", "application/json")

	email := r.URL.Query().Get("email")       // получаю параметр email
	password := r.URL.Query().Get("password") // получаю параметр password

	log.Println(email, password)

	// проверка на правильность email
	if isValidEmail(email) {
		fmt.Printf("%s - верный формат email\n", email)
	} else {
		http.Error(w, "Invalid email", http.StatusMethodNotAllowed)
		fmt.Printf("%s - неверный формат email\n", email)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	activationCode, err := generateActivationCode() // генерируется код активации
	if err != nil {
		http.Error(w, "Failed to generate activation code", http.StatusInternalServerError)
		return
	}

	var dat Data

	dat = Data{time.Now().Add(time.Duration(30) * time.Second), activationCode, password} // в переменной хранится объект типа Data, где есть время, код и пароль

	// Сохраняем код активации в сессии пользователя, используя email пользователя как ключ
	sessions[email] = dat

	// Отправляем код активации на email пользователя
	sendActivationEmail(email, activationCode)
	if err != nil {
		// Ошибка отправки email - это не ошибка регистрации, поэтому возвращать ошибку не будем
		fmt.Printf("Ошибка отправки email для подтверждения регистрации: %v\n", err)
	}

	fmt.Fprint(w, "true")
}

// функция для генирации кода для регистрации на сайте
func generateActivationCode() (string, error) {
	randoms := make([]byte, 8) // создание среза, содержащего 8 элемента(0). Размер в байтах для кода, 8 байт

	_, err := rand.Read(randoms) // заполняет срез случайными значениями

	if err != nil {
		return "", err
	}
	return hex.EncodeToString(randoms), nil // возвращает строку, в которой каждый байт среза конвертируется в его двухсимвольное шестнадцатеричное представление.
}

// функция для отправки кода на email пользователя
func sendActivationEmail(toEmail, code string) error {
	// Настройки SMTP-сервера
	smtpHost := "smtp.inbox.ru" // адрес SMTP-сервера для почтового провайдера inbox.ru
	smtpPort := 587             // стандартный порт для безопасности
	smtpUsername := "tasker_site@inbox.ru"
	smtpPassword := "8HxZgtHkDQ6tg2Vuwppw"

	// Настройки электронного письма
	subject := "Подтверждение регистрации" // тема письма
	body := fmt.Sprintf("Ваш код регистрации: %s\n", code)

	// Формирование сообщения
	message := fmt.Sprintf("To: %s\r\n", toEmail) +
		fmt.Sprintf("Subject: %s\r\n", subject) +
		"\r\n" + body

	// Аутентификация на SMTP-сервере
	auth := smtp.PlainAuth("", smtpUsername, smtpPassword, smtpHost)

	// Отправка письма
	err := smtp.SendMail(fmt.Sprintf("%s:%d", smtpHost, smtpPort), auth, smtpUsername, []string{toEmail}, []byte(message))

	if err != nil {
		fmt.Println("Ошибка отправки письма:", err)
		return nil
	}

	fmt.Println("Письмо успешно отправлено!")

	return nil
}

func sendDeadlineReminderEmail(toEmail string, task Task) error {
	smtpHost := "smtp.inbox.ru" // адрес SMTP-сервера для почтового провайдера inbox.ru
	smtpPort := 587             // стандартный порт для безопасности
	smtpUsername := "tasker_site@inbox.ru"
	smtpPassword := "8HxZgtHkDQ6tg2Vuwppw"

	// Настройки электронного письма
	subject := "Напоминание о приближении крайнего срока." // тема письма
	body := fmt.Sprintf("Выставленный вами дедлайн наступит менее, чем через 24 часа.\nНазвание задачи: %s\nОписание: %s\n", task.Title, task.Description)

	// Формирование сообщения
	message := fmt.Sprintf("To: %s\r\n", toEmail) +
		fmt.Sprintf("Subject: %s\r\n", subject) +
		"\r\n" + body

	// Аутентификация на SMTP-сервере
	auth := smtp.PlainAuth("", smtpUsername, smtpPassword, smtpHost)

	// Отправка письма
	//err := smtp.SendMail(smtpHost+":"+string(smtpPort), auth, smtpUsername, []string{toEmail}, []byte(message))
	err := smtp.SendMail(fmt.Sprintf("%s:%d", smtpHost, smtpPort), auth, smtpUsername, []string{toEmail}, []byte(message))
	if err != nil {
		fmt.Println("Ошибка отправки письма:", err)
		return nil
	}

	log.Println("Письмо успешно отправлено!")

	return nil

}

// проверка валидности email c помощью встроенной функции
func isValidEmail(email string) bool {
	_, err := mail.ParseAddress(email)
	if err != nil {
		return false
	}
	return true
}

func regFinish(w http.ResponseWriter, r *http.Request) { // функция принимает код, отправленный пользователем, и делает проверку на время истечения и правильность
	email := r.URL.Query().Get("email")
	code := r.URL.Query().Get("code")

	session := sessions

	if time.Now().After(session[email].T) { // если время истекло
		fmt.Fprint(w, "time out")
		delete(session, email)
		return
	}

	if session[email].Code == code {
		fmt.Fprint(w, "true")
		addUserToBd(email, session[email].Password, []Task{}) // добавить пользователя в БД
		delete(session, email)
		return
	}
}

// добавляем пользователя в БД
func addUserToBd(email, password string, tasks []Task) error {
	// Параметры подключения к MongoDB
	clientOptions := options.Client().ApplyURI("mongodb+srv://peremotka:oiS5dwR7A8@taskerusers.abxenef.mongodb.net/?retryWrites=true&w=majority")

	// Установка соединения с MongoDB
	client, err := mongo.Connect(context.Background(), clientOptions)
	if err != nil {
		return err
	}

	// Проверка соединения с MongoDB
	err = client.Ping(context.Background(), nil)
	if err != nil {
		return err
	}

	// Получение коллекции "users"
	usersCollection := client.Database("taskerUsers").Collection("Users")

	// Создание нового пользователя
	user := User{
		Email:    email,
		Password: password,
		Tasks:    tasks,
	}

	// Вставка пользователя в коллекцию "users"
	_, err = usersCollection.InsertOne(context.Background(), user)
	if err != nil {
		return err
	}

	fmt.Println("Пользователь добавлен в базу данных MongoDB")
	return nil
}

// получение пользователя из базы данных
func getUserFromBd(email string) (User, error) {
	// Параметры подключения к MongoDB
	clientOptions := options.Client().ApplyURI("mongodb+srv://peremotka:oiS5dwR7A8@taskerusers.abxenef.mongodb.net/?retryWrites=true&w=majority")

	// Установка соединения с MongoDB
	client, err := mongo.Connect(context.Background(), clientOptions)
	if err != nil {
		return User{}, err
	}

	// Проверка соединения с MongoDB
	err = client.Ping(context.Background(), nil)
	if err != nil {
		return User{}, err
	}

	// Получение коллекции "users"
	usersCollection := client.Database("taskerUsers").Collection("Users")

	// Поиск пользователя с указанным email
	var user User
	err = usersCollection.FindOne(context.Background(), bson.M{"email": email}).Decode(&user)
	if err != nil {
		return User{}, err
	}

	return user, nil
}

// получение всех пользователей из базы данных
func getAllUsersFromBd() ([]User, error) {
	// Параметры подключения к MongoDB
	clientOptions := options.Client().ApplyURI("mongodb+srv://peremotka:oiS5dwR7A8@taskerusers.abxenef.mongodb.net/?retryWrites=true&w=majority")

	// Установка соединения с MongoDB
	client, err := mongo.Connect(context.Background(), clientOptions)
	if err != nil {
		return []User{}, err
	}

	// Проверка соединения с MongoDB
	err = client.Ping(context.Background(), nil)
	if err != nil {
		return []User{}, err
	}

	// Получение коллекции "users"
	usersCollection := client.Database("taskerUsers").Collection("Users")

	// Определение фильтра для поиска всех пользователей
	filter := bson.D{}

	// Выполнение запроса
	cursor, err := usersCollection.Find(context.Background(), filter) // создается курсор, который представляет собой результат запроса к коллекции
	if err != nil {
		return []User{}, err
	}

	defer cursor.Close(context.Background())

	// Декодирование результатов запроса
	var users []User

	// итерация по результатам запроса.
	for cursor.Next(context.Background()) { // переходит к следующему результату
		var user User
		err := cursor.Decode(&user) // декодирование текущего результата в экземпляр структуры User
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}

	return users, nil

}

func login(res http.ResponseWriter, req *http.Request) {
	email := req.URL.Query().Get("email")       // получаю параметр email
	password := req.URL.Query().Get("password") // получаю параметр password

	// Проверка наличия пользователя с указанным email в базе данных
	user, err := getUserFromBd(email)
	if err != nil {
		fmt.Fprint(res, "false")
		return
	}

	// Проверка правильности пароля
	if user.Password != password {
		fmt.Fprint(res, "false")
		return
	}
	fmt.Fprint(res, "true") // возвращаю true
}

func changePassword(res http.ResponseWriter, req *http.Request) {
	email := req.URL.Query().Get("email")
	newPassword := req.URL.Query().Get("new_password")

	// Параметры подключения к MongoDB
	clientOptions := options.Client().ApplyURI("mongodb+srv://peremotka:oiS5dwR7A8@taskerusers.abxenef.mongodb.net/?retryWrites=true&w=majority")

	// Установка соединения с MongoDB
	client, err := mongo.Connect(context.Background(), clientOptions)
	if err != nil {
		http.Error(res, err.Error(), http.StatusBadRequest)
		return
	}

	// Проверка соединения с MongoDB
	err = client.Ping(context.Background(), nil)
	if err != nil {
		http.Error(res, err.Error(), http.StatusBadRequest)
		return
	}

	// Получение коллекции "users"
	usersCollection := client.Database("taskerUsers").Collection("Users")

	filter := bson.D{{"email", email}} // bson.D представляет упорядоченный набор пар "ключ-значение"

	// Обновление пароля пользователя
	update := bson.M{"$set": bson.M{"password": newPassword}} // bson.M представляет неупорядоченный набор пар "ключ-значение"
	_, err = usersCollection.UpdateOne(context.Background(), filter, update)
	if err != nil {
		fmt.Fprint(res, "Не удалось обновить пароль пользователя из коллекции", err)
		return
	}

	fmt.Printf("Пароль пользователя %s успешно изменен\n", email)
	fmt.Fprintf(res, "Пароль пользователя %s успешно изменен\n", email)

}

func deleteUser(res http.ResponseWriter, req *http.Request) {
	email := req.URL.Query().Get("email")

	// Параметры подключения к MongoDB
	clientOptions := options.Client().ApplyURI("mongodb+srv://peremotka:oiS5dwR7A8@taskerusers.abxenef.mongodb.net/?retryWrites=true&w=majority")

	// Установка соединения с MongoDB
	client, err := mongo.Connect(context.Background(), clientOptions)
	if err != nil {
		http.Error(res, err.Error(), http.StatusBadRequest)
		return
	}

	// Проверка соединения с MongoDB
	err = client.Ping(context.Background(), nil)
	if err != nil {
		http.Error(res, err.Error(), http.StatusBadRequest)
		return
	}

	// Получение коллекции "users"
	usersCollection := client.Database("taskerUsers").Collection("Users")

	filter := bson.D{{"email", email}}

	// Удаляем пользователя из коллекции
	_, err = usersCollection.DeleteOne(context.TODO(), filter)
	if err != nil {
		fmt.Fprint(res, "Не удалось удалить пользователя из коллекции", err)
		return
	}

	fmt.Fprint(res, "Пользователь успешно удален")
}

func deadlineTimer() {
	for { // бесконечный цикл for для постоянной проверки
		// Периодически проверяем все задачи и отправляем уведомления
		users, err := getAllUsersFromBd()
		if err != nil {
			log.Println("Ошибка получения пользователей из базы данных:", err)
			continue
		}

		for _, user := range users {
			for _, task := range user.Tasks {
				// Проверяем, если время выполнения задачи приближается
				timeBeforeDeadline := task.Deadline.Sub(time.Now()) // метод sub вычисляет разницу между нынешним временем и дедлайном
				// Если остается менее 24 часов до дедлайна и задача не выполнена, отправляем уведомление
				if timeBeforeDeadline.Hours() < 24 && task.Complete == false {
					err := sendDeadlineReminderEmail(user.Email, task)
					if err != nil {
						log.Println("Ошибка отправки уведомления:", err)
					}
				}
			}
		}

		// функция для задержки выполнения программы на определенное время
		time.Sleep(time.Hour) // проверка каждый час

	}
}
