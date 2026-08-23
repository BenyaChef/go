package main

import (
	"cmp"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const AccessToken = "token"

var datasetPath = "dataset.xml"

const (
	errBadOrderField = "ErrorBadOrderField"
	errBadOrderBy    = "ErrorBadOrderBy"
	errBadLimit      = "ErrorBadLimit"
	errBadOffset     = "ErrorBadOffset"
)

type XMLUser struct {
	ID        int    `xml:"id"`
	FirstName string `xml:"first_name"`
	LastName  string `xml:"last_name"`
	Gender    string `xml:"gender"`
	Age       int    `xml:"age"`
	About     string `xml:"about"`
}

type Root struct {
	XMLName xml.Name  `xml:"root"`
	Users   []XMLUser `xml:"row"`
}

func SearchServer(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("AccessToken") != AccessToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	req, err := parseSearchRequest(r.URL)
	if err != nil {
		writeSearchError(w, http.StatusBadRequest, err.Error())
		return
	}

	users, err := loadUsers(datasetPath)
	if err != nil {
		writeSearchError(w, http.StatusInternalServerError, err.Error())
		return
	}

	users = filterUsers(users, req.Query)

	if err := sortUsers(users, req.OrderField, req.OrderBy); err != nil {
		writeSearchError(w, http.StatusBadRequest, err.Error())
		return
	}

	users = paginate(users, req.Offset, req.Limit)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(users); err != nil {
		writeSearchError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeSearchError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(SearchErrorResponse{Error: msg})
}

func parseSearchRequest(u *url.URL) (SearchRequest, error) {
	q := u.Query()

	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit < 0 {
		return SearchRequest{}, errors.New(errBadLimit)
	}

	offset, err := strconv.Atoi(q.Get("offset"))
	if err != nil || offset < 0 {
		return SearchRequest{}, errors.New(errBadOffset)
	}

	orderBy, err := strconv.Atoi(q.Get("order_by"))
	if err != nil {
		return SearchRequest{}, errors.New(errBadOrderBy)
	}

	return SearchRequest{
		Query:      q.Get("query"),
		OrderField: q.Get("order_field"),
		OrderBy:    orderBy,
		Limit:      limit,
		Offset:     offset,
	}, nil
}

func loadUsers(path string) ([]User, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var root Root
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, err
	}

	users := make([]User, 0, len(root.Users))
	for _, x := range root.Users {
		users = append(users, User{
			Id:     x.ID,
			Name:   x.FirstName + " " + x.LastName,
			Age:    x.Age,
			About:  x.About,
			Gender: x.Gender,
		})
	}

	return users, nil
}

func filterUsers(users []User, query string) []User {
	out := make([]User, 0, len(users))
	for _, u := range users {
		if strings.Contains(u.Name, query) || strings.Contains(u.About, query) {
			out = append(out, u)
		}
	}
	return out
}

func sortUsers(users []User, orderField string, orderBy int) error {
	var cmpFunc func(a, b User) int

	switch orderField {
	case "Id":
		cmpFunc = func(a, b User) int { return cmp.Compare(a.Id, b.Id) }
	case "Age":
		cmpFunc = func(a, b User) int { return cmp.Compare(a.Age, b.Age) }
	case "Name", "":
		cmpFunc = func(a, b User) int { return cmp.Compare(a.Name, b.Name) }
	default:
		return errors.New(errBadOrderField)
	}

	switch orderBy {
	case OrderByAsIs:
	case OrderByAsc:
		slices.SortFunc(users, cmpFunc)
	case OrderByDesc:
		slices.SortFunc(users, func(a, b User) int { return cmpFunc(b, a) })
	default:
		return errors.New(errBadOrderBy)
	}

	return nil
}

func paginate(users []User, offset, limit int) []User {
	if offset >= len(users) {
		return users[:0]
	}
	users = users[offset:]

	if limit < len(users) {
		users = users[:limit]
	}
	return users
}

func newTestClient(t *testing.T, h http.HandlerFunc) *SearchClient {
	t.Helper()

	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	return &SearchClient{AccessToken: AccessToken, URL: ts.URL}
}

func seq(from, to int) []int {
	out := make([]int, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

func TestFindUsers(t *testing.T) {
	client := newTestClient(t, SearchServer)

	cases := []struct {
		name     string
		req      SearchRequest
		wantIDs  []int
		wantNext bool
	}{
		{
			name:     "сортировка по Name по возрастанию",
			req:      SearchRequest{Limit: 3, OrderField: "Name", OrderBy: OrderByAsc},
			wantIDs:  []int{15, 16, 19},
			wantNext: true,
		},
		{
			name:     "сортировка по Name по убыванию",
			req:      SearchRequest{Limit: 3, OrderField: "Name", OrderBy: OrderByDesc},
			wantIDs:  []int{13, 33, 18},
			wantNext: true,
		},
		{
			name:     "пустой order_field сортирует по Name",
			req:      SearchRequest{Limit: 3, OrderField: "", OrderBy: OrderByAsc},
			wantIDs:  []int{15, 16, 19},
			wantNext: true,
		},
		{
			name:     "сортировка по Id по возрастанию",
			req:      SearchRequest{Limit: 5, OrderField: "Id", OrderBy: OrderByAsc},
			wantIDs:  []int{0, 1, 2, 3, 4},
			wantNext: true,
		},
		{
			name:     "сортировка по Id по убыванию со сдвигом",
			req:      SearchRequest{Limit: 2, Offset: 3, OrderField: "Id", OrderBy: OrderByDesc},
			wantIDs:  []int{31, 30},
			wantNext: true,
		},
		{
			name:     "OrderByAsIs сохраняет порядок файла",
			req:      SearchRequest{Limit: 4, OrderField: "Name", OrderBy: OrderByAsIs},
			wantIDs:  []int{0, 1, 2, 3},
			wantNext: true,
		},
		{
			name:     "поиск по имени",
			req:      SearchRequest{Query: "Boyd", Limit: 10, OrderField: "Id", OrderBy: OrderByAsc},
			wantIDs:  []int{0},
			wantNext: false,
		},
		{
			name:     "поиск по фамилии находит несколько записей",
			req:      SearchRequest{Query: "Guerr", Limit: 10, OrderField: "Id", OrderBy: OrderByAsc},
			wantIDs:  []int{11, 12},
			wantNext: false,
		},
		{
			name:     "поиск по полю About",
			req:      SearchRequest{Query: "Nulla", Limit: 10, OrderField: "Id", OrderBy: OrderByAsc},
			wantIDs:  []int{0, 2, 19, 21},
			wantNext: false,
		},
		{
			name:     "ничего не найдено",
			req:      SearchRequest{Query: "zzzz_nothing", Limit: 10, OrderField: "Id", OrderBy: OrderByAsc},
			wantIDs:  []int{},
			wantNext: false,
		},
		{
			name:     "offset за пределами выборки",
			req:      SearchRequest{Offset: 100, Limit: 5, OrderField: "Id", OrderBy: OrderByAsc},
			wantIDs:  []int{},
			wantNext: false,
		},
		{
			name:     "limit больше 25 обрезается клиентом до 25",
			req:      SearchRequest{Limit: 100, OrderField: "Id", OrderBy: OrderByAsc},
			wantIDs:  seq(0, 24),
			wantNext: true,
		},
		{
			name:     "последняя страница — NextPage false",
			req:      SearchRequest{Limit: 25, Offset: 10, OrderField: "Id", OrderBy: OrderByAsc},
			wantIDs:  seq(10, 34),
			wantNext: false,
		},
		{
			name:     "выборка ровно до конца датасета",
			req:      SearchRequest{Limit: 5, Offset: 30, OrderField: "Id", OrderBy: OrderByAsc},
			wantIDs:  seq(30, 34),
			wantNext: false,
		},
		{
			name:     "limit 0 не возвращает записей, но следующая страница есть",
			req:      SearchRequest{Limit: 0, OrderField: "Id", OrderBy: OrderByAsc},
			wantIDs:  []int{},
			wantNext: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.FindUsers(tc.req)
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if resp.NextPage != tc.wantNext {
				t.Errorf("NextPage: получили %v, ожидали %v", resp.NextPage, tc.wantNext)
			}
		})
	}
}

func TestFindUsersSortedByAge(t *testing.T) {
	client := newTestClient(t, SearchServer)

	cases := []struct {
		name     string
		orderBy  int
		wantEdge int
		sorted   func(a, b User) bool
	}{
		{
			name:     "по возрастанию",
			orderBy:  OrderByAsc,
			wantEdge: 21,
			sorted:   func(a, b User) bool { return a.Age <= b.Age },
		},
		{
			name:     "по убыванию",
			orderBy:  OrderByDesc,
			wantEdge: 40,
			sorted:   func(a, b User) bool { return a.Age >= b.Age },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.FindUsers(SearchRequest{Limit: 25, OrderField: "Age", OrderBy: tc.orderBy})
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if len(resp.Users) != 25 {
				t.Fatalf("ожидали 25 пользователей, получили %d", len(resp.Users))
			}
			if resp.Users[0].Age != tc.wantEdge {
				t.Errorf("возраст первой записи: получили %d, ожидали %d", resp.Users[0].Age, tc.wantEdge)
			}
		})
	}
}

func TestFindUsersInvalidParams(t *testing.T) {
	client := newTestClient(t, SearchServer)

	cases := []struct {
		name    string
		req     SearchRequest
		wantErr string
	}{
		{
			name:    "отрицательный limit",
			req:     SearchRequest{Limit: -1},
			wantErr: "limit must be > 0",
		},
		{
			name:    "отрицательный offset",
			req:     SearchRequest{Limit: 1, Offset: -1},
			wantErr: "offset must be > 0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.FindUsers(tc.req)
			if resp != nil {
				t.Errorf("ожидали nil-результат, получили %+v", resp)
			}
			if err == nil {
				t.Fatal("ожидали ошибку, получили nil")
			}
			if err.Error() != tc.wantErr {
				t.Errorf("текст ошибки: получили %q, ожидали %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestFindUsersBadAccessToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(SearchServer))
	defer ts.Close()

	client := &SearchClient{AccessToken: "wrong-token", URL: ts.URL}

	resp, err := client.FindUsers(SearchRequest{Limit: 1})
	if resp != nil {
		t.Errorf("ожидали nil-результат, получили %+v", resp)
	}
	if err == nil || err.Error() != "Bad AccessToken" {
		t.Fatalf("ожидали ошибку %q, получили %v", "Bad AccessToken", err)
	}
}

func TestFindUsersBadOrderField(t *testing.T) {
	client := newTestClient(t, SearchServer)

	resp, err := client.FindUsers(SearchRequest{Limit: 1, OrderField: "Gender", OrderBy: OrderByAsc})
	if resp != nil {
		t.Errorf("ожидали nil-результат, получили %+v", resp)
	}
	want := "OrderFeld Gender invalid"
	if err == nil || err.Error() != want {
		t.Fatalf("ожидали ошибку %q, получили %v", want, err)
	}
}

func TestFindUsersBadOrderBy(t *testing.T) {
	client := newTestClient(t, SearchServer)

	resp, err := client.FindUsers(SearchRequest{Limit: 1, OrderField: "Id", OrderBy: 42})
	if resp != nil {
		t.Errorf("ожидали nil-результат, получили %+v", resp)
	}
	want := "unknown bad request error: " + errBadOrderBy
	if err == nil || err.Error() != want {
		t.Fatalf("ожидали ошибку %q, получили %v", want, err)
	}
}

func TestFindUsersServerErrors(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{
			name: "500 от сервера",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			wantErr: "SearchServer fatal error",
		},
		{
			name: "400 с неразбираемым телом",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"Error": broken`))
			},
			wantErr: "cant unpack error json: invalid character 'b' looking for beginning of value",
		},
		{
			name: "200 с неразбираемым телом",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`[{"Id": "не число"}]`))
			},
			wantErr: "cant unpack result json: json: cannot unmarshal string into Go struct field User.Id of type int",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, tc.handler)

			resp, err := client.FindUsers(SearchRequest{Limit: 1, OrderField: "Id"})
			if resp != nil {
				t.Errorf("ожидали nil-результат, получили %+v", resp)
			}
			if err == nil {
				t.Fatal("ожидали ошибку, получили nil")
			}
			if err.Error() != tc.wantErr {
				t.Errorf("текст ошибки:\nполучили %q\nожидали  %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestFindUsersTimeout(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1100 * time.Millisecond)
	})

	resp, err := client.FindUsers(SearchRequest{Limit: 1, OrderField: "Id"})
	if resp != nil {
		t.Errorf("ожидали nil-результат, получили %+v", resp)
	}
	if err == nil {
		t.Fatal("ожидали ошибку таймаута, получили nil")
	}
	if !strings.HasPrefix(err.Error(), "timeout for ") {
		t.Errorf("ожидали ошибку таймаута, получили %q", err.Error())
	}
}

func TestFindUsersUnknownError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(SearchServer))
	ts.Close()

	client := &SearchClient{AccessToken: AccessToken, URL: ts.URL}

	resp, err := client.FindUsers(SearchRequest{Limit: 1, OrderField: "Id"})
	if resp != nil {
		t.Errorf("ожидали nil-результат, получили %+v", resp)
	}
	if err == nil {
		t.Fatal("ожидали ошибку соединения, получили nil")
	}
	if !strings.HasPrefix(err.Error(), "unknown error ") {
		t.Errorf("ожидали ошибку соединения, получили %q", err.Error())
	}
}
